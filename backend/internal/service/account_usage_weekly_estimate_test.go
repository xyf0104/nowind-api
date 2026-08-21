package service

import (
	"math"
	"testing"
	"time"
)

func TestOpenAIWeeklyEstimateRequiresCompleteTwoPercentInterval(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(1)
	resetAt := openAIWeeklyEstimateTestResetAt()
	initial := openAIWeeklyEstimateProgress(8, 220, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, initial)
	requireOpenAIWeeklyEstimatePending(t, initial)
	onePoint := openAIWeeklyEstimateProgress(9, 258, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, onePoint)
	requireOpenAIWeeklyEstimatePending(t, onePoint)
	complete := openAIWeeklyEstimateProgress(10, 300, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, complete)
	requireOpenAIWeeklyEstimate(t, complete, 300+90*40)
	requireOpenAIWeeklyEstimateSamples(t, account, []float64{40})
	incomplete := openAIWeeklyEstimateProgress(11, 350, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, incomplete)
	requireOpenAIWeeklyEstimate(t, incomplete, 3900)
}

func TestOpenAIWeeklyEstimateUsesNonZeroBaselineAndMissingPoints(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(2)
	resetAt := openAIWeeklyEstimateTestResetAt()
	login := openAIWeeklyEstimateProgress(26, 100, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, login)
	requireOpenAIWeeklyEstimatePending(t, login)
	jump := openAIWeeklyEstimateProgress(28, 180, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, jump)
	requireOpenAIWeeklyEstimate(t, jump, 180+72*40)
	requireOpenAIWeeklyEstimateState(t, account, 28, 180, 28, 180)
}

func TestOpenAIWeeklyEstimateUsesCurrentCostAtCompletedBoundary(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(3)
	resetAt := openAIWeeklyEstimateTestResetAt()
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(40, 200, resetAt))
	complete := openAIWeeklyEstimateProgress(43, 320, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, complete)
	requireOpenAIWeeklyEstimate(t, complete, 320+57*40)
	requireOpenAIWeeklyEstimateSamples(t, account, []float64{40})
}

func TestOpenAIWeeklyEstimateRebasesExternalUsage(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(4)
	resetAt := openAIWeeklyEstimateTestResetAt()
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 100, resetAt))
	trusted := openAIWeeklyEstimateProgress(22, 140, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, trusted)
	requireOpenAIWeeklyEstimate(t, trusted, 140+78*20)
	externalOnly := openAIWeeklyEstimateProgress(23, 140, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, externalOnly)
	requireOpenAIWeeklyEstimate(t, externalOnly, 1700)
	requireOpenAIWeeklyEstimateState(t, account, 23, 140, 23, 140)
	requireOpenAIWeeklyEstimateSamples(t, account, []float64{20})
	localIncomplete := openAIWeeklyEstimateProgress(24, 160, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, localIncomplete)
	requireOpenAIWeeklyEstimate(t, localIncomplete, 1700)
	localComplete := openAIWeeklyEstimateProgress(25, 180, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, localComplete)
	requireOpenAIWeeklyEstimate(t, localComplete, 180+75*20)
	requireOpenAIWeeklyEstimateSamples(t, account, []float64{20, 20})
}

func TestOpenAIWeeklyEstimateUsesRobustRecencyWeightedSamples(t *testing.T) {
	t.Parallel()
	rate, ok := smartOpenAIWeeklyEstimateRate([]float64{10, 10, 100})
	if !ok || math.Abs(rate-10) > 0.5 {
		t.Fatalf("robust rate = %v, want approximately 10", rate)
	}
	rate, ok = smartOpenAIWeeklyEstimateRate([]float64{10, 20, 30})
	if !ok || rate <= 20 || rate >= 24 {
		t.Fatalf("recency-weighted rate = %v, want slightly above 20", rate)
	}
}

func TestOpenAIWeeklyEstimateKeepsNewestFiveSamples(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(6)
	resetAt := openAIWeeklyEstimateTestResetAt()
	percent, cost := 0.0, 0.0
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(percent, cost, resetAt))
	for sample := 1.0; sample <= 6; sample++ {
		percent += 2
		cost += sample * 2
		applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(percent, cost, resetAt))
	}
	requireOpenAIWeeklyEstimateSamples(t, account, []float64{2, 3, 4, 5, 6})
}

func TestOpenAIWeeklyEstimateRestartsAfterResetIdentityReauthorizationOrRollback(t *testing.T) {
	resetAt := openAIWeeklyEstimateTestResetAt()
	cases := []struct {
		name     string
		mutate   func(*Account)
		progress func(time.Time) *UsageProgress
	}{
		{name: "window reset", progress: func(reset time.Time) *UsageProgress {
			return openAIWeeklyEstimateProgress(12, 50, reset.Add(7*24*time.Hour))
		}},
		{name: "identity change", mutate: func(account *Account) {
			account.Credentials["chatgpt_account_id"] = "workspace-b"
		}, progress: func(reset time.Time) *UsageProgress {
			return openAIWeeklyEstimateProgress(52, 1100, reset)
		}},
		{name: "reauthorization", mutate: func(account *Account) {
			delete(account.Extra, openAIWeeklyEstimateBaselineKey)
		}, progress: func(reset time.Time) *UsageProgress {
			return openAIWeeklyEstimateProgress(52, 1100, reset)
		}},
		{name: "percent rollback", progress: func(reset time.Time) *UsageProgress {
			return openAIWeeklyEstimateProgress(50.5, 1070, reset)
		}},
		{name: "cost rollback", progress: func(reset time.Time) *UsageProgress {
			return openAIWeeklyEstimateProgress(52, 1050, reset)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(20)
			applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(50, 1000, resetAt))
			trusted := openAIWeeklyEstimateProgress(52, 1060, resetAt)
			applyOpenAIWeeklyEstimateForTest(t, account, trusted)
			requireOpenAIWeeklyEstimate(t, trusted, 1060+48*30)
			if tc.mutate != nil {
				tc.mutate(account)
			}
			observed := tc.progress(resetAt)
			applyOpenAIWeeklyEstimateForTest(t, account, observed)
			requireOpenAIWeeklyEstimatePending(t, observed)
		})
	}
}

func TestOpenAIWeeklyEstimateMigratesLegacyStateWithoutInventingSamples(t *testing.T) {
	t.Parallel()
	resetAt := openAIWeeklyEstimateTestResetAt()
	account := newOpenAIWeeklyEstimateTestAccount(7)
	account.Extra[openAIWeeklyEstimateBaselineKey] = map[string]any{
		"version": 5, "segment_percent": 38.0, "segment_cost": 142.0,
		"last_percent": 38.5, "last_cost": 160.0, "segment_max_estimate": 3800.0,
		"completed_estimate": 5000.0, "reset_at": resetAt.Format(time.RFC3339Nano),
		"identity": "workspace-a",
	}
	migrated := openAIWeeklyEstimateProgress(38.5, 160, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, migrated)
	requireOpenAIWeeklyEstimate(t, migrated, 5000)
	requireOpenAIWeeklyEstimateSamples(t, account, nil)
	complete := openAIWeeklyEstimateProgress(40.5, 200, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, complete)
	requireOpenAIWeeklyEstimateSamples(t, account, []float64{23.2})
	requireOpenAIWeeklyEstimate(t, complete, 200+59.5*23.2)
}

func TestOpenAIWeeklyEstimateUsesUnroundedObservations(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(8)
	resetAt := openAIWeeklyEstimateTestResetAt()
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(12.345, 10.123, resetAt))
	complete := openAIWeeklyEstimateProgress(14.345, 42.789, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, complete)
	requireOpenAIWeeklyEstimate(t, complete, 42.789+(100-14.345)*((42.789-10.123)/2))
}

func TestOpenAIWeeklyEstimateAtFullQuotaAlwaysEqualsAccountCost(t *testing.T) {
	t.Parallel()
	resetAt := openAIWeeklyEstimateTestResetAt()
	account := newOpenAIWeeklyEstimateTestAccount(9)
	full := openAIWeeklyEstimateProgress(100, 108.05, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, full)
	requireOpenAIWeeklyEstimate(t, full, 108.05)
	zeroCostAccount := newOpenAIWeeklyEstimateTestAccount(10)
	zeroCost := openAIWeeklyEstimateProgress(100, 0, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, zeroCostAccount, zeroCost)
	requireOpenAIWeeklyEstimate(t, zeroCost, 0)
}

func newOpenAIWeeklyEstimateTestAccount(id int64) *Account {
	return &Account{ID: id, Credentials: map[string]any{"chatgpt_account_id": "workspace-a"}, Extra: map[string]any{}}
}

func openAIWeeklyEstimateTestResetAt() time.Time {
	return time.Date(2026, time.August, 25, 5, 17, 50, 123456789, time.UTC)
}

func applyOpenAIWeeklyEstimateForTest(t *testing.T, account *Account, progress *UsageProgress) {
	t.Helper()
	estimate, updates := calculateOpenAIWeeklyEstimate(account, progress)
	progress.WeeklyEstimateUSD = estimate
	if len(updates) > 0 {
		mergeAccountExtra(account, updates)
	}
}

func openAIWeeklyEstimateProgress(percent, cost float64, resetAt time.Time) *UsageProgress {
	return &UsageProgress{Utilization: percent, ResetsAt: &resetAt, WindowStats: &WindowStats{Cost: cost}}
}

func requireOpenAIWeeklyEstimate(t *testing.T, progress *UsageProgress, want float64) {
	t.Helper()
	if progress.WeeklyEstimateUSD == nil {
		t.Fatal("weekly estimate is nil")
	}
	if math.Abs(*progress.WeeklyEstimateUSD-want) > 1e-9 {
		t.Fatalf("weekly estimate = %v, want %v", *progress.WeeklyEstimateUSD, want)
	}
}

func requireOpenAIWeeklyEstimatePending(t *testing.T, progress *UsageProgress) {
	t.Helper()
	if progress.WeeklyEstimateUSD != nil {
		t.Fatalf("weekly estimate = %v, want collecting state", *progress.WeeklyEstimateUSD)
	}
}

func requireOpenAIWeeklyEstimateState(t *testing.T, account *Account, segmentPercent, segmentCost, lastPercent, lastCost float64) {
	t.Helper()
	raw := requireOpenAIWeeklyEstimateRawState(t, account)
	if version := parseExtraInt(raw["version"]); version != openAIWeeklyEstimateStateVersion {
		t.Fatalf("state version = %d, want %d", version, openAIWeeklyEstimateStateVersion)
	}
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "segment_percent", segmentPercent)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "segment_cost", segmentCost)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "last_percent", lastPercent)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "last_cost", lastCost)
}

func requireOpenAIWeeklyEstimateSamples(t *testing.T, account *Account, want []float64) {
	t.Helper()
	raw := requireOpenAIWeeklyEstimateRawState(t, account)
	got, ok := parseOpenAIWeeklyEstimateSamples(raw["samples"])
	if !ok || len(got) != len(want) {
		t.Fatalf("samples = %#v, want %#v", got, want)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("samples = %#v, want %#v", got, want)
		}
	}
}

func requireOpenAIWeeklyEstimateRawState(t *testing.T, account *Account) map[string]any {
	t.Helper()
	raw, ok := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if !ok {
		t.Fatal("weekly estimate state is missing")
	}
	return raw
}

func requireOpenAIWeeklyEstimateStateNumber(t *testing.T, raw map[string]any, key string, want float64) {
	t.Helper()
	got, ok := parseOpenAIWeeklyEstimateNumber(raw, key)
	if !ok || math.Abs(got-want) > 1e-9 {
		t.Fatalf("state %s = %v, want %v", key, got, want)
	}
}
