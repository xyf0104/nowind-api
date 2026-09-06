package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// sparkShadowUsageTestRepo is a minimal AccountRepository stub for spark shadow
// usage tests.  GetByID serves both shadow and parent accounts from a map;
// UpdateExtra records the persisted updates for assertion.
type sparkShadowUsageTestRepo struct {
	AccountRepository
	accounts      map[int64]*Account
	updateExtraCh chan map[string]any
}

func (r *sparkShadowUsageTestRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if acc, ok := r.accounts[id]; ok {
		return acc, nil
	}
	return nil, fmt.Errorf("account %d not found", id)
}

func (r *sparkShadowUsageTestRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if r.updateExtraCh != nil {
		copied := make(map[string]any, len(updates))
		for k, v := range updates {
			copied[k] = v
		}
		r.updateExtraCh <- copied
	}
	return nil
}

func (r *sparkShadowUsageTestRepo) UpdateOpenAICodexSnapshot(_ context.Context, _ int64, updates map[string]any) (bool, error) {
	if r.updateExtraCh != nil {
		copied := make(map[string]any, len(updates))
		for k, v := range updates {
			copied[k] = v
		}
		r.updateExtraCh <- copied
	}
	return true, nil
}

func (r *sparkShadowUsageTestRepo) UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx context.Context, target, credential *Account, _ time.Time, updates map[string]any) (bool, error) {
	if !quotaTestIdentityMatches(r.accounts[target.ID], target) || !quotaTestIdentityMatches(r.accounts[credential.ID], credential) {
		return false, nil
	}
	return true, r.UpdateExtra(ctx, target.ID, updates)
}

func TestGetOpenAIUsage_SparkShadow_WritesExtraAndReturnsNonEmptyWindows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pid := int64(100)
	shadow := &Account{
		ID:              200,
		ParentAccountID: &pid,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		Status:          StatusActive,
		QuotaDimension:  QuotaDimensionSpark,
		Extra: map[string]any{
			"codex_5h_used_percent": 7.0, "codex_7d_used_percent": 8.0,
			"codex_usage_updated_at": "2026-01-01T00:00:00Z",
		},
	}
	parent := &Account{
		ID:       100,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"chatgpt_account_id": "org-spark-parent",
			"access_token":       "fake-access-token",
		},
	}

	// Repo shared by both the OpenAIQuotaService (needs shadow+parent for resolve)
	// and the AccountUsageService (needs UpdateExtra for persist).
	updateExtraCh := make(chan map[string]any, 1)
	repo := &sparkShadowUsageTestRepo{
		accounts:      map[int64]*Account{200: shadow, 100: parent},
		updateExtraCh: updateExtraCh,
	}

	// Token cache: return a fake token for the parent account key.
	tokenCache := &stubQuotaTokenCache{tokens: map[string]string{
		OpenAITokenCacheKey(parent): "fake-access-token",
	}}
	tokenProvider := NewOpenAITokenProvider(repo, tokenCache, nil)

	// httptest server: records the chatgpt-account-id header and returns a
	// synthetic OpenAIQuotaUsage with codex_bengalfox 5h+7d windows.
	var capturedAccountID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAccountID = r.Header.Get("chatgpt-account-id")
		w.Header().Set("content-type", "application/json")
		resp := OpenAIQuotaUsage{
			AdditionalRateLimits: []OpenAIAdditionalRateLimit{
				{
					MeteredFeature: "codex_bengalfox",
					RateLimit: &OpenAIRateLimit{
						// Primary window → 5h (18000 s = 300 min)
						PrimaryWindow: &OpenAIRateLimitWindow{
							UsedPercent:        42.5,
							ResetAfterSeconds:  3600,
							LimitWindowSeconds: 18000,
						},
						// Secondary window → 7d (604800 s = 10080 min)
						SecondaryWindow: &OpenAIRateLimitWindow{
							UsedPercent:        10.0,
							ResetAfterSeconds:  86400,
							LimitWindowSeconds: 604800,
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	quotaService := NewOpenAIQuotaService(repo, nil, tokenProvider, newQuotaRedirectingFactory(srv))
	svc := &AccountUsageService{
		accountRepo:        repo,
		openAIQuotaService: quotaService,
	}

	usage, err := svc.getOpenAIUsage(ctx, shadow, true /*force*/)
	require.NoError(t, err)

	// Assertion A-1: upstream received the PARENT's chatgpt-account-id.
	require.Equal(t, "org-spark-parent", capturedAccountID,
		"QueryUsage must use parent's chatgpt-account-id for spark shadow accounts")

	select {
	case updates := <-updateExtraCh:
		require.Equal(t, 42.5, updates["codex_5h_used_percent"])
		require.Equal(t, 10.0, updates["codex_7d_used_percent"])
	default:
		t.Fatal("bound Spark query did not persist")
	}
	require.Equal(t, 42.5, shadow.Extra["codex_5h_used_percent"])
	require.Equal(t, 10.0, shadow.Extra["codex_7d_used_percent"])
	require.NotNil(t, usage.FiveHour)
	require.NotNil(t, usage.SevenDay)
	require.Equal(t, 42.5, usage.FiveHour.Utilization)
	require.Equal(t, 10.0, usage.SevenDay.Utilization)
}
