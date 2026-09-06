package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newWeeklyJoinCreateAccount() *Account {
	return &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Schedulable: true,
		Credentials: map[string]any{"access_token": "synthetic-token", "chatgpt_account_id": "synthetic-account"},
		Extra:       map[string]any{"codex_fingerprint_mode": "off", "custom": true}}
}

func TestOpenAIWeeklyJoinCreateCapturesZeroAndMidpointBeforeLocalUsage(t *testing.T) {
	for _, percent := range []float64{0, 11, 20.4} {
		t.Run(strconv.FormatFloat(percent, 'f', -1, 64), func(t *testing.T) {
			var calls atomic.Int64
			reset := time.Now().Add(5 * 24 * time.Hour).Unix()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/backend-api/wham/usage", r.URL.Path)
				require.Equal(t, "synthetic-account", r.Header.Get("Chatgpt-Account-Id"))
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"account_id": "synthetic-account", "rate_limit": map[string]any{
					"secondary_window": map[string]any{"used_percent": percent, "limit_window_seconds": 604800, "reset_at": reset}}}))
			}))
			defer server.Close()
			account := newWeeklyJoinCreateAccount()
			svc := &adminServiceImpl{privacyClientFactory: newQuotaRedirectingFactory(server)}
			svc.captureOpenAIWeeklyJoinForCreate(context.Background(), account)
			require.EqualValues(t, 1, calls.Load())
			state, ok := readOpenAIWeeklyFrozenEstimateState(account.Extra)
			require.True(t, ok)
			require.Equal(t, percent, state.BaselinePercent)
			require.Zero(t, state.BaselineCost)
			require.Equal(t, "pre_create_quota_read", state.BaselineSource)
			if percent == 0 {
				require.Equal(t, openAIWeeklyEstimateModeLegacy, state.Mode)
			} else {
				require.Equal(t, openAIWeeklyEstimateModeJoinAverage, state.Mode)
			}
			require.False(t, state.HasEstimate)
			require.True(t, account.Schedulable)
			require.Equal(t, "off", account.Extra["codex_fingerprint_mode"])
		})
	}
}

func TestOpenAIWeeklyJoinCreateRejectsUnprovenPayload(t *testing.T) {
	for _, name := range []string{"missing percent", "wrong identity", "not weekly", "negative", "unknown reset", "http error"} {
		t.Run(name, func(t *testing.T) {
			window := map[string]any{"used_percent": 20, "limit_window_seconds": 604800, "reset_at": time.Now().Add(time.Hour).Unix()}
			identity := "synthetic-account"
			switch name {
			case "missing percent":
				delete(window, "used_percent")
			case "wrong identity":
				identity = "another-account"
			case "not weekly":
				window["limit_window_seconds"] = 18000
			case "negative":
				window["used_percent"] = -1
			case "unknown reset":
				delete(window, "reset_at")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if name == "http error" {
					w.WriteHeader(http.StatusUnauthorized)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"account_id": identity, "rate_limit": map[string]any{"primary_window": window}})
			}))
			defer server.Close()
			account := newWeeklyJoinCreateAccount()
			account.Schedulable = false
			svc := &adminServiceImpl{privacyClientFactory: newQuotaRedirectingFactory(server)}
			svc.captureOpenAIWeeklyJoinForCreate(context.Background(), account)
			require.NotContains(t, account.Extra, openAIWeeklyEstimateBaselineKey)
			require.False(t, account.Schedulable)
			require.Equal(t, true, account.Extra["custom"])
		})
	}
}

func TestOpenAIWeeklyJoinCreateDoesNotProbeExistingOrUnresolvedProxy(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	svc := &adminServiceImpl{privacyClientFactory: newQuotaRedirectingFactory(server)}
	account := newWeeklyJoinCreateAccount()
	account.ID = 42
	svc.captureOpenAIWeeklyJoinForCreate(context.Background(), account)
	account.ID = 0
	proxyID := int64(99)
	account.ProxyID = &proxyID
	svc.captureOpenAIWeeklyJoinForCreate(context.Background(), account)
	require.Zero(t, calls.Load(), "no existing-account reset or direct-egress fallback")
}

type weeklyJoinCreateSpyRepo struct {
	AccountRepository
	t *testing.T
}

func (r *weeklyJoinCreateSpyRepo) Create(_ context.Context, account *Account) error {
	state, ok := readOpenAIWeeklyFrozenEstimateState(account.Extra)
	require.True(r.t, ok, "capture must precede the row becoming schedulable")
	require.Equal(r.t, 20.0, state.BaselinePercent)
	require.Zero(r.t, state.BaselineCost)
	return errors.New("synthetic write stopped")
}

func TestOpenAIWeeklyJoinCreateHookDiscardsImportedState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"rate_limit": map[string]any{"primary_window": map[string]any{
			"used_percent": 20, "limit_window_seconds": 604800, "reset_at": time.Now().Add(time.Hour).Unix()}}})
	}))
	defer server.Close()
	svc := &adminServiceImpl{accountRepo: &weeklyJoinCreateSpyRepo{t: t}, privacyClientFactory: newQuotaRedirectingFactory(server)}
	_, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name: "synthetic", Platform: PlatformOpenAI, Type: AccountTypeOAuth, SkipDefaultGroupBind: true,
		Credentials: newWeeklyJoinCreateAccount().Credentials,
		Extra:       map[string]any{openAIWeeklyEstimateBaselineKey: map[string]any{"invented": true}, "codex_7d_used_percent": 0.0},
	})
	require.EqualError(t, err, "synthetic write stopped")
}

func TestOpenAIWeeklyJoinCreateBudgetDoesNotAccumulatePerAccount(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()
	svc := &adminServiceImpl{privacyClientFactory: newQuotaRedirectingFactory(server)}
	ctx := WithOpenAIWeeklyJoinCaptureBudget(context.Background(), 100*time.Millisecond)
	started := time.Now()
	for range 8 {
		account := newWeeklyJoinCreateAccount()
		svc.captureOpenAIWeeklyJoinForCreate(ctx, account)
		require.NotContains(t, account.Extra, openAIWeeklyEstimateBaselineKey)
		require.NoError(t, ctx.Err(), "budget exhaustion must not abort database creation")
	}
	require.Less(t, time.Since(started), 2*time.Second)
	require.LessOrEqual(t, calls.Load(), int64(1), "one shared budget, not eight five-second requests")
	boundedAgain := WithOpenAIWeeklyJoinCaptureBudget(ctx, time.Hour)
	svc.captureOpenAIWeeklyJoinForCreate(boundedAgain, newWeeklyJoinCreateAccount())
	require.LessOrEqual(t, calls.Load(), int64(1), "nested calls cannot extend the exhausted budget")
}
