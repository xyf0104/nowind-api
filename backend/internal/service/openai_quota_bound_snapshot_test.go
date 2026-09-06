package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func quotaTestIdentityMatches(current, expected *Account) bool {
	if current == nil || expected == nil {
		return false
	}
	a, err := cloneOpenAICodexSnapshotIdentity(current)
	if err != nil {
		return false
	}
	b, err := cloneOpenAICodexSnapshotIdentity(expected)
	if err != nil {
		return false
	}
	a.QuotaDimension, b.QuotaDimension = a.QuotaDimensionOrDefault(), b.QuotaDimensionOrDefault()
	return reflect.DeepEqual(a, b)
}

func quotaTestRequestIdentity() *openAIQuotaRequestIdentity {
	account := &Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "synthetic-old-access", "refresh_token": "synthetic-old-refresh", "chatgpt_account_id": "synthetic-account"}}
	return &openAIQuotaRequestIdentity{target: account, credential: account, observedAt: time.Date(2026, 9, 6, 1, 2, 3, 123456000, time.UTC)}
}

func quotaTestResponse() *OpenAIQuotaUsage {
	rate := &OpenAIRateLimit{
		PrimaryWindow:   &OpenAIRateLimitWindow{UsedPercent: 12, LimitWindowSeconds: 18000, ResetAfterSeconds: 3600},
		SecondaryWindow: &OpenAIRateLimitWindow{UsedPercent: 34, LimitWindowSeconds: 604800, ResetAfterSeconds: 86400},
	}
	return &OpenAIQuotaUsage{
		RateLimit: rate, AdditionalRateLimits: []OpenAIAdditionalRateLimit{{MeteredFeature: "codex_bengalfox", RateLimit: rate}},
		RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 0},
	}
}

func TestBoundQuotaQueryParentReauthCrossesOldResponse(t *testing.T) {
	for _, shadowQuery := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "spark"}[shadowQuery], func(t *testing.T) {
			parent := quotaTestRequestIdentity().target
			target := parent
			if shadowQuery {
				target = &Account{ID: 200, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, QuotaDimension: QuotaDimensionSpark}
			}
			repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{parent.ID: parent, target.ID: target}}
			started, release := make(chan struct{}), make(chan struct{})
			headers := make(chan string, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				headers <- r.Header.Get("Authorization")
				if strings.HasSuffix(r.URL.Path, "/usage") {
					close(started)
					<-release
					_ = json.NewEncoder(w).Encode(quotaTestResponse())
				} else {
					_, _ = w.Write([]byte(`{"credits":[]}`))
				}
			}))
			defer server.Close()
			provider := NewOpenAITokenProvider(repo, &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(parent): "synthetic-old-access"}}, nil)
			svc := NewOpenAIQuotaService(repo, nil, provider, newQuotaRedirectingFactory(server))
			type result struct {
				usage *OpenAIQuotaUsage
				err   error
			}
			done := make(chan result, 1)
			go func() { usage, err := svc.QueryUsage(context.Background(), target.ID); done <- result{usage, err} }()
			<-started
			// The already-started request owns the old credentials. The details
			// request must not reread and silently switch to the new identity.
			parent.Credentials = map[string]any{"access_token": "synthetic-new-access", "refresh_token": "synthetic-new-refresh", "chatgpt_account_id": "synthetic-new-account"}
			close(release)
			got := <-done
			require.NoError(t, got.err)
			require.Equal(t, "Bearer synthetic-old-access", <-headers)
			require.Equal(t, "Bearer synthetic-old-access", <-headers)
			require.Equal(t, "synthetic-old-access", got.usage.requestIdentity.credential.GetOpenAIAccessToken())
			require.ErrorContains(t, svc.CachePostResetSnapshot(context.Background(), target.ID, got.usage), "no longer current")
			require.ErrorContains(t, svc.CacheResetCreditsSnapshot(context.Background(), target.ID, got.usage.RateLimitResetCredits), "no longer current")
			patch := buildCodexSparkWindowExtraUpdates(got.usage, got.usage.requestIdentity.observedAt)
			patch["codex_usage_updated_at"] = got.usage.requestIdentity.observedAt.Format(time.RFC3339Nano)
			applied, err := (&AccountUsageService{accountRepo: repo}).persistOpenAICodexProbeSnapshot(target, patch, got.usage.requestIdentity)
			require.NoError(t, err)
			require.False(t, applied)
			require.Empty(t, repo.extraUpdates)
		})
	}
}

func TestBoundQuotaQueryCapturesObservationAndNeverSerializesCredentials(t *testing.T) {
	parent := quotaTestRequestIdentity().target
	shadow := &Account{ID: 200, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, QuotaDimension: QuotaDimensionSpark}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{100: parent, 200: shadow}}
	var detailsStartedAt time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/usage") {
			_ = json.NewEncoder(w).Encode(quotaTestResponse())
		} else {
			detailsStartedAt = time.Now().UTC()
			_, _ = w.Write([]byte(`{"credits":[]}`))
		}
	}))
	defer server.Close()
	svc := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(parent): "synthetic-old-access"}}, nil), newQuotaRedirectingFactory(server))
	usage, err := svc.QueryUsage(context.Background(), shadow.ID)
	require.NoError(t, err)
	identity := usage.requestIdentity
	require.False(t, identity.observedAt.IsZero())
	require.True(t, identity.observedAt.Before(detailsStartedAt))
	require.Equal(t, identity.observedAt.Unix(), usage.FetchedAt)
	require.NoError(t, svc.CachePostResetSnapshot(context.Background(), shadow.ID, usage))
	patch := repo.extraUpdates[shadow.ID]
	require.Equal(t, identity.observedAt.Format(time.RFC3339Nano), patch["codex_usage_updated_at"])
	require.Equal(t, identity.observedAt.Format(time.RFC3339Nano), patch[openaiQuotaResetCreditsTimeKey])
	require.Equal(t, 34.0, patch["codex_7d_used_percent"])
	require.NoError(t, svc.CacheResetCreditsSnapshot(context.Background(), shadow.ID, usage.RateLimitResetCredits))
	require.NotContains(t, repo.extraUpdates[shadow.ID], "codex_usage_updated_at", "credits alone must not advance raw quota observation")
	encoded, err := json.Marshal(usage)
	require.NoError(t, err)
	for _, private := range []string{"requestIdentity", "credential", "synthetic-old-access", "synthetic-old-refresh", "observedAt"} {
		require.NotContains(t, string(encoded), private)
	}
	var roundTrip OpenAIQuotaUsage
	require.NoError(t, json.Unmarshal(encoded, &roundTrip))
	require.Nil(t, roundTrip.requestIdentity)
	require.Nil(t, roundTrip.RateLimitResetCredits.requestIdentity)
	require.ErrorContains(t, svc.CachePostResetSnapshot(context.Background(), shadow.ID, &roundTrip), "no captured request identity")
	require.Error(t, svc.CacheResetCreditsSnapshot(context.Background(), parent.ID, usage.RateLimitResetCredits), "target ID cannot be substituted")
}

func TestBoundQuotaQueryTokenProviderMismatchRequiresFreshRequest(t *testing.T) {
	account := quotaTestRequestIdentity().target
	repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{100: account}}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "Bearer synthetic-refreshed", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(quotaTestResponse())
	}))
	defer server.Close()
	svc := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(account): "synthetic-refreshed"}}, nil), newQuotaRedirectingFactory(server))
	_, err := svc.QueryUsage(context.Background(), account.ID)
	require.ErrorContains(t, err, "does not match the captured credentials")
	require.Zero(t, calls)
	require.NotContains(t, err.Error(), "synthetic-refreshed")
	account.Credentials["access_token"] = "synthetic-refreshed"
	usage, err := svc.QueryUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "synthetic-refreshed", usage.requestIdentity.credential.GetOpenAIAccessToken())
	require.Positive(t, calls)
}

func TestBoundQuotaCacheUnknownRepositoryHasNoLegacyFallback(t *testing.T) {
	repo := &legacyOnlyCodexSnapshotTestRepo{}
	svc := &OpenAIQuotaService{accountRepo: repo}
	identity := quotaTestRequestIdentity()
	usage := quotaTestResponse()
	usage.requestIdentity, usage.RateLimitResetCredits.requestIdentity = identity, identity
	require.ErrorContains(t, svc.CachePostResetSnapshot(context.Background(), identity.target.ID, usage), "identity-bound OpenAI quota")
	require.ErrorContains(t, svc.CacheResetCreditsSnapshot(context.Background(), identity.target.ID, usage.RateLimitResetCredits), "identity-bound OpenAI quota")
	require.Zero(t, repo.writes)
}
