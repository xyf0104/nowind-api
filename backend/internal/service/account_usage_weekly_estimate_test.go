package service

import (
	"math"
	"testing"
	"time"
)

func TestOpenAIWeeklyEstimateUsesLatestCompletedOnePercentAndBoundaryMaximum(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(1)
	resetAt := openAIWeeklyEstimateTestResetAt()

	initial := openAIWeeklyEstimateProgress(10, 20, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, initial)
	requireOpenAIWeeklyEstimatePending(t, initial)

	// Cost can rise while the upstream percentage still displays 10%. The
	// interval must retain the original $20 start rather than moving the start.
	insideInterval := openAIWeeklyEstimateProgress(10, 35, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, insideInterval)
	requireOpenAIWeeklyEstimatePending(t, insideInterval)

	firstBoundary := openAIWeeklyEstimateProgress(11, 45, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, firstBoundary)
	requireOpenAIWeeklyEstimate(t, firstBoundary, 45+89*25)

	// The maximum cumulative cost observed at the completed 11% boundary wins:
	// $50 used plus ($50-$20) for each of the remaining 89 percentage points.
	boundaryMaximum := openAIWeeklyEstimateProgress(11, 50, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, boundaryMaximum)
	requireOpenAIWeeklyEstimate(t, boundaryMaximum, 2720)
	requireOpenAIWeeklyEstimateState(t, account, 10, 20, 11, 50)
}

func TestOpenAIWeeklyEstimateImputesMissingPrefixForMidWindowLogin(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(2)
	resetAt := openAIWeeklyEstimateTestResetAt()

	login := openAIWeeklyEstimateProgress(20, 0, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, login)
	requireOpenAIWeeklyEstimatePending(t, login)

	insideInterval := openAIWeeklyEstimateProgress(20, 25, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, insideInterval)
	requireOpenAIWeeklyEstimatePending(t, insideInterval)

	firstBoundary := openAIWeeklyEstimateProgress(21, 25, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, firstBoundary)
	requireOpenAIWeeklyEstimate(t, firstBoundary, 2500)

	complete := openAIWeeklyEstimateProgress(21, 30, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, complete)
	// 20% before login is estimated as 20*$30=$600. The observed point is $30
	// and the remaining 79 points are $2370, so the total is exactly $3000.
	requireOpenAIWeeklyEstimate(t, complete, 3000)
	raw := requireOpenAIWeeklyEstimateRawState(t, account)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "prefix_percent", 20)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "prefix_estimated_cost", 600)
}

func TestOpenAIWeeklyEstimateUsesOnlyNewestCompletedInterval(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(3)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(10, 20, resetAt))
	first := openAIWeeklyEstimateProgress(11, 50, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, first)
	requireOpenAIWeeklyEstimate(t, first, 2720)

	// A partial next interval keeps the last completed estimate.
	partial := openAIWeeklyEstimateProgress(11.4, 65, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, partial)
	requireOpenAIWeeklyEstimate(t, partial, 2720)

	// The 11% boundary was frozen at $50 when utilization advanced. Therefore
	// 11%->$50 through 12%->$95 makes the newest complete interval $45/%.
	second := openAIWeeklyEstimateProgress(12, 95, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, second)
	requireOpenAIWeeklyEstimate(t, second, 95+88*45)
}

func TestOpenAIWeeklyEstimateUsesObservableAverageWhenQuotaJumps(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(4)
	resetAt := openAIWeeklyEstimateTestResetAt()
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(26, 100, resetAt))
	jump := openAIWeeklyEstimateProgress(28, 180, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, jump)
	requireOpenAIWeeklyEstimate(t, jump, 180+72*40)
}

func TestOpenAIWeeklyEstimateRebasesPureExternalUsage(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(5)
	resetAt := openAIWeeklyEstimateTestResetAt()
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 100, resetAt))

	external := openAIWeeklyEstimateProgress(21, 100, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, external)
	requireOpenAIWeeklyEstimatePending(t, external)
	requireOpenAIWeeklyEstimateState(t, account, 21, 100, 0, 0)

	local := openAIWeeklyEstimateProgress(22, 130, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, local)
	requireOpenAIWeeklyEstimate(t, local, 130+78*30)
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
			return openAIWeeklyEstimateProgress(12, 80, reset)
		}},
		{name: "reauthorization", mutate: func(account *Account) {
			delete(account.Extra, openAIWeeklyEstimateBaselineKey)
		}, progress: func(reset time.Time) *UsageProgress {
			return openAIWeeklyEstimateProgress(12, 80, reset)
		}},
		{name: "percent rollback", progress: func(reset time.Time) *UsageProgress {
			return openAIWeeklyEstimateProgress(10.5, 70, reset)
		}},
		{name: "cost rollback", progress: func(reset time.Time) *UsageProgress {
			return openAIWeeklyEstimateProgress(12, 45, reset)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(20)
			applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(10, 20, resetAt))
			trusted := openAIWeeklyEstimateProgress(11, 50, resetAt)
			applyOpenAIWeeklyEstimateForTest(t, account, trusted)
			requireOpenAIWeeklyEstimate(t, trusted, 2720)
			if tc.mutate != nil {
				tc.mutate(account)
			}
			observed := tc.progress(resetAt)
			applyOpenAIWeeklyEstimateForTest(t, account, observed)
			requireOpenAIWeeklyEstimatePending(t, observed)
		})
	}
}

func TestOpenAIWeeklyEstimateRebasesLegacyAlgorithmState(t *testing.T) {
	t.Parallel()
	resetAt := openAIWeeklyEstimateTestResetAt()
	account := newOpenAIWeeklyEstimateTestAccount(7)
	account.Extra[openAIWeeklyEstimateBaselineKey] = map[string]any{
		"version": 6, "segment_percent": 38.0, "segment_cost": 142.0,
		"last_percent": 40.0, "last_cost": 200.0, "samples": []float64{29},
		"completed_estimate": 5000.0, "reset_at": resetAt.Format(time.RFC3339Nano),
		"identity": "workspace-a",
	}

	observed := openAIWeeklyEstimateProgress(40, 200, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, observed)
	requireOpenAIWeeklyEstimatePending(t, observed)
	raw := requireOpenAIWeeklyEstimateRawState(t, account)
	if version := parseExtraInt(raw["version"]); version != openAIWeeklyEstimateStateVersion {
		t.Fatalf("state version = %d, want %d", version, openAIWeeklyEstimateStateVersion)
	}
	if _, exists := raw["samples"]; exists {
		t.Fatal("legacy smoothed samples must not survive the v7 rebase")
	}
}

func TestOpenAIWeeklyEstimateUsesUnroundedObservations(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(8)
	resetAt := openAIWeeklyEstimateTestResetAt()
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(12.345, 10.123, resetAt))
	complete := openAIWeeklyEstimateProgress(13.345, 42.789, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, complete)
	requireOpenAIWeeklyEstimate(t, complete, 42.789+(100-13.345)*(42.789-10.123))
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

func requireOpenAIWeeklyEstimateState(t *testing.T, account *Account, segmentPercent, segmentCost, completedPercent, completedCost float64) {
	t.Helper()
	raw := requireOpenAIWeeklyEstimateRawState(t, account)
	if version := parseExtraInt(raw["version"]); version != openAIWeeklyEstimateStateVersion {
		t.Fatalf("state version = %d, want %d", version, openAIWeeklyEstimateStateVersion)
	}
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "segment_percent", segmentPercent)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "segment_cost", segmentCost)
	if completedPercent == 0 && completedCost == 0 {
		if _, ok := raw["completed_percent"]; ok {
			t.Fatalf("unexpected completed boundary: %#v", raw)
		}
		return
	}
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "completed_percent", completedPercent)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "completed_cost", completedCost)
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
