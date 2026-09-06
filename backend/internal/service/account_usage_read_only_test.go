package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

// Every unimplemented method panics, including all writes, error clearing,
// snapshot persistence and estimator range reads.
type readOnlyUsageAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *readOnlyUsageAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if id != r.account.ID {
		return nil, errors.New("passive usage must not resolve another account")
	}
	return r.account, nil
}

type readOnlyUsageStatsRepo struct {
	UsageLogRepository
	stats  *usagestats.AccountStats
	err    error
	starts []time.Time
}

func (r *readOnlyUsageStatsRepo) GetAccountWindowStats(_ context.Context, _ int64, start time.Time) (*usagestats.AccountStats, error) {
	r.starts = append(r.starts, start)
	return r.stats, r.err
}

func readOnlyUsageFixture() (*AccountUsageService, *Account, *readOnlyUsageStatsRepo) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	resetAt := now.Add(5 * 24 * time.Hour)
	state := openAIWeeklyFrozenEstimateState{
		Mode: openAIWeeklyEstimateModeJoinAverage, BaselineSource: "observed_mid_join",
		BaselinePercent: 20, BaselineCost: 0, PercentBucket: 25,
		SnapshotPercent: 25, SnapshotCost: 120, CompletedPercent: 25, CompletedCost: 120,
		EstimateUSD: 2400, HasEstimate: true,
		ResetAt: resetAt, Identity: "read-only-account", ObservedAt: now.Add(-time.Hour),
	}
	account := &Account{
		ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusError, ErrorMessage: "temporary upstream error",
		RateLimitResetAt: &resetAt,
		Credentials:      map[string]any{"access_token": "test-token", "chatgpt_account_id": state.Identity},
		Extra: map[string]any{
			"codex_5h_used_percent":                        12.5,
			"codex_5h_reset_at":                            now.Add(time.Hour).Format(time.RFC3339Nano),
			"codex_7d_used_percent":                        25.4,
			"codex_7d_reset_at":                            resetAt.Format(time.RFC3339Nano),
			"codex_usage_updated_at":                       state.ObservedAt.Format(time.RFC3339Nano),
			"openai_oauth_responses_websockets_v2_enabled": true,
			openAIWeeklyEstimateBaselineKey:                openAIWeeklyFrozenEstimateStateUpdate(state)[openAIWeeklyEstimateBaselineKey],
		},
	}
	stats := &readOnlyUsageStatsRepo{stats: &usagestats.AccountStats{
		Requests: 20, Tokens: 300, Cost: 123.4567, StandardCost: 111.2222, UserCost: 45.6789,
	}}
	svc := &AccountUsageService{
		accountRepo: &readOnlyUsageAccountRepo{account: account}, usageLogRepo: stats, cache: NewUsageCache(),
	}
	return svc, account, stats
}

func TestOpenAIPassiveUsageReadsSharedSnapshotWithoutUpstreamOrWrites(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		t.Run(strconv.FormatBool(shadow), func(t *testing.T) {
			svc, account, stats := readOnlyUsageFixture()
			var upstreamCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamCalls.Add(1)
				w.WriteHeader(http.StatusProxyAuthRequired)
			}))
			defer server.Close()
			proxyURL, err := url.Parse(server.URL)
			require.NoError(t, err)
			port, err := strconv.Atoi(proxyURL.Port())
			require.NoError(t, err)
			proxyID := int64(1)
			account.ProxyID = &proxyID
			account.Proxy = &Proxy{Protocol: "http", Host: proxyURL.Hostname(), Port: port}
			if shadow {
				parentID := int64(1)
				account.ParentAccountID = &parentID
				account.QuotaDimension = QuotaDimensionSpark
				// QueryUsage would need to resolve the parent and fail this test.
				svc.openAIQuotaService = NewOpenAIQuotaService(svc.accountRepo, nil, nil, newQuotaRedirectingFactory(server))
			}
			before, err := json.Marshal(account)
			require.NoError(t, err)
			usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.Equal(t, "passive", usage.Source)
			require.NotNil(t, usage.UpdatedAt)
			require.Equal(t, account.Extra["codex_usage_updated_at"], usage.UpdatedAt.Format(time.RFC3339Nano))
			require.Equal(t, 12.5, usage.FiveHour.Utilization)
			require.Equal(t, 25.4, usage.SevenDay.Utilization)
			require.Equal(t, windowStatsFromAccountStats(stats.stats), usage.FiveHour.WindowStats)
			require.Equal(t, windowStatsFromAccountStats(stats.stats), usage.SevenDay.WindowStats)
			require.NotNil(t, usage.SevenDay.WeeklyEstimateUSD)
			require.Equal(t, 2400.0, *usage.SevenDay.WeeklyEstimateUSD)
			require.Equal(t, []time.Time{usage.FiveHour.ResetsAt.Add(-5 * time.Hour), usage.SevenDay.ResetsAt.Add(-7 * 24 * time.Hour)}, stats.starts)
			require.Zero(t, upstreamCalls.Load())
			svc.cache.openAIProbeCache.Range(func(_, _ any) bool { t.Error("passive usage populated probe cache"); return false })
			svc.openAIProbeStates.Range(func(_, _ any) bool { t.Error("passive usage created probe state"); return false })
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestOpenAIPassiveUsageOnlyDisplaysMatchingPersistedEstimate(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Account, *readOnlyUsageStatsRepo)
	}{
		{"no state", func(a *Account, _ *readOnlyUsageStatsRepo) { delete(a.Extra, openAIWeeklyEstimateBaselineKey) }},
		{"identity changed", func(a *Account, _ *readOnlyUsageStatsRepo) { a.Credentials["chatgpt_account_id"] = "another-account" }},
		{"percentage regressed", func(a *Account, _ *readOnlyUsageStatsRepo) { a.Extra["codex_7d_used_percent"] = 24.9 }},
		{"owner estimate pending", func(a *Account, _ *readOnlyUsageStatsRepo) { a.Extra["codex_7d_used_percent"] = 26.0 }},
		{"cost regressed", func(_ *Account, r *readOnlyUsageStatsRepo) { r.stats.Cost = 100 }},
		{"expired window", func(a *Account, _ *readOnlyUsageStatsRepo) {
			a.Extra["codex_7d_reset_at"] = time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
		}},
		{"new window", func(a *Account, _ *readOnlyUsageStatsRepo) {
			a.Extra["codex_7d_reset_at"] = time.Now().Add(7 * 24 * time.Hour).Format(time.RFC3339Nano)
		}},
		{"missing observation", func(a *Account, _ *readOnlyUsageStatsRepo) { delete(a.Extra, "codex_usage_updated_at") }},
		{"older observation", func(a *Account, _ *readOnlyUsageStatsRepo) {
			a.Extra["codex_usage_updated_at"] = time.Now().Add(-2 * time.Hour).Format(time.RFC3339Nano)
		}},
		{"legacy state", func(a *Account, _ *readOnlyUsageStatsRepo) {
			a.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)["version"] = openAIWeeklyFrozenEstimateLegacyStateVersion
		}},
		{"stats unavailable", func(_ *Account, r *readOnlyUsageStatsRepo) { r.err = errors.New("database unavailable") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, account, stats := readOnlyUsageFixture()
			test.change(account, stats)
			before, err := json.Marshal(account)
			require.NoError(t, err)
			usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.NotNil(t, usage.SevenDay)
			require.Nil(t, usage.SevenDay.WeeklyEstimateUSD)
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestOpenAIPassiveUsageCompleteWindowIsExactIncludingZero(t *testing.T) {
	for _, cost := range []float64{0, 123.456789} {
		svc, account, stats := readOnlyUsageFixture()
		account.Extra["codex_7d_used_percent"] = 100.0
		delete(account.Extra, openAIWeeklyEstimateBaselineKey)
		stats.stats.Cost = cost
		usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
		require.NoError(t, err)
		require.NotNil(t, usage.SevenDay.WeeklyEstimateUSD)
		require.Equal(t, cost, *usage.SevenDay.WeeklyEstimateUSD)
	}
}

func TestOpenAIPassiveUsageMissingWindowsDoesNotCreateWeeklyState(t *testing.T) {
	svc, account, stats := readOnlyUsageFixture()
	account.Extra = nil
	usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.Nil(t, usage.SevenDay)
	require.Nil(t, usage.UpdatedAt)
	require.Zero(t, usage.FiveHour.Utilization)
	require.Equal(t, windowStatsFromAccountStats(stats.stats), usage.FiveHour.WindowStats)
	require.Len(t, stats.starts, 1)
	require.Nil(t, account.Extra)
}
