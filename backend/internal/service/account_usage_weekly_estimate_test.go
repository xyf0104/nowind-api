package service

import (
	"math"
	"testing"
	"time"
)

func TestOpenAIWeeklyEstimateUsesCumulativeReadableAverage(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(1)
	resetAt := openAIWeeklyEstimateTestResetAt()

	// The first readable observation is the fixed baseline for this 7d window.
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 30, resetAt))
	first := openAIWeeklyEstimateProgress(21, 40, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, first)
	requireOpenAIWeeklyEstimate(t, first, 1000)

	// More cost at the same upstream percentage does not change the estimate.
	// The next completed percentage uses its own current cumulative cost.
	insideTwentyOne := openAIWeeklyEstimateProgress(21, 50, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, insideTwentyOne)
	requireOpenAIWeeklyEstimate(t, insideTwentyOne, 1000)

	atTwentyTwo := openAIWeeklyEstimateProgress(22, 55, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, atTwentyTwo)
	requireOpenAIWeeklyEstimate(t, atTwentyTwo, 1250)

	atTwentyThree := openAIWeeklyEstimateProgress(23, 58, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, atTwentyThree)
	requireOpenAIWeeklyEstimate(t, atTwentyThree, 28.0/3.0*100)
	requireOpenAIWeeklyEstimateV10State(t, account, 20, 30, 23, 58, 23)
	raw := requireOpenAIWeeklyEstimateRawState(t, account)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "last_estimated_cost", 58)
}

func TestOpenAIWeeklyEstimateConvertsObservedCumulativeAverageToWeeklyQuota(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(101)
	resetAt := openAIWeeklyEstimateTestResetAt()

	// These are the three observed values from the reported account. The
	// estimate must use the readable 33%/$699.34 origin, not the old latest-1%
	// extrapolation that produced 1243 -> 2754 -> 808.
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(33, 699.34, resetAt))
	atThirtyFour := openAIWeeklyEstimateProgress(34, 700.96, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, atThirtyFour)
	requireOpenAIWeeklyEstimate(t, atThirtyFour, (700.96-699.34)*100)

	atThirtyFive := openAIWeeklyEstimateProgress(35, 743.43, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, atThirtyFive)
	requireOpenAIWeeklyEstimate(t, atThirtyFive, (743.43-699.34)/2*100)
}

func TestOpenAIWeeklyEstimateWaitsForReadableCostAtNonzeroStartingPercent(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(2)
	resetAt := openAIWeeklyEstimateTestResetAt()

	// An account added at 20% with no local cost keeps the percentage as the
	// pending start but does not invent a cost for earlier usage.
	initial := openAIWeeklyEstimateProgress(20, 0, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, initial)
	requireOpenAIWeeklyEstimatePending(t, initial)
	requireOpenAIWeeklyEstimateV10HasNoBaseline(t, account, 20, 0)

	// As soon as the 20% point has an actual XIASS cost, it becomes the baseline.
	readableAtTwenty := openAIWeeklyEstimateProgress(20, 30, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, readableAtTwenty)
	requireOpenAIWeeklyEstimatePending(t, readableAtTwenty)
	requireOpenAIWeeklyEstimateV10State(t, account, 20, 30, 20, 30, 20)

	atTwentyOne := openAIWeeklyEstimateProgress(21, 40, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, atTwentyOne)
	requireOpenAIWeeklyEstimate(t, atTwentyOne, 1000)
}

func TestOpenAIWeeklyEstimateUsesFullObservedDeltaWhenPercentagesAreSkipped(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(3)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 30, resetAt))
	skipped := openAIWeeklyEstimateProgress(22, 55, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, skipped)

	// $25 across two readable percentage points is $12.50 per point.
	requireOpenAIWeeklyEstimate(t, skipped, 1250)
}

func TestOpenAIWeeklyEstimateRebasesAfterUnbilledPercentAdvance(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(31)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 30, resetAt))
	first := openAIWeeklyEstimateProgress(21, 40, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, first)
	requireOpenAIWeeklyEstimate(t, first, 1000)

	// The provider percentage moved but XIASS did not bill anything. Keep the
	// previous credible result and sample anew from this point.
	external := openAIWeeklyEstimateProgress(22, 40, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, external)
	requireOpenAIWeeklyEstimate(t, external, 1000)
	requireOpenAIWeeklyEstimateV10State(t, account, 22, 40, 22, 40, 22)

	localAgain := openAIWeeklyEstimateProgress(23, 50, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, localAgain)
	requireOpenAIWeeklyEstimate(t, localAgain, 1000)
}

func TestOpenAIWeeklyEstimatePreservesSameIdentityAcrossReauthorization(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(4)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 30, resetAt))
	first := openAIWeeklyEstimateProgress(21, 40, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, first)
	requireOpenAIWeeklyEstimate(t, first, 1000)

	// Reauthorization rotates credentials but keeps the upstream account identity.
	account.Credentials["access_token"] = "rotated-access-token"
	account.Credentials["refresh_token"] = "rotated-refresh-token"
	afterReauthorization := openAIWeeklyEstimateProgress(22, 55, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, afterReauthorization)
	requireOpenAIWeeklyEstimate(t, afterReauthorization, 1250)
	requireOpenAIWeeklyEstimateV10State(t, account, 20, 30, 22, 55, 22)
}

func TestOpenAIWeeklyEstimateRebasesForRealStateBreaks(t *testing.T) {
	resetAt := openAIWeeklyEstimateTestResetAt()
	cases := []struct {
		name     string
		mutate   func(*Account)
		progress func() *UsageProgress
	}{
		{
			name: "expired window",
			progress: func() *UsageProgress {
				return openAIWeeklyEstimateProgress(22, 55, time.Now().UTC().Add(-time.Minute))
			},
		},
		{
			name: "new weekly deadline",
			progress: func() *UsageProgress {
				return openAIWeeklyEstimateProgress(22, 55, resetAt.Add(7*24*time.Hour))
			},
		},
		{
			name: "identity change",
			mutate: func(account *Account) {
				account.Credentials["chatgpt_account_id"] = "workspace-b"
			},
			progress: func() *UsageProgress {
				return openAIWeeklyEstimateProgress(22, 55, resetAt)
			},
		},
		{
			name: "percent rollback",
			progress: func() *UsageProgress {
				return openAIWeeklyEstimateProgress(20, 55, resetAt)
			},
		},
		{
			name: "account cost rollback",
			progress: func() *UsageProgress {
				return openAIWeeklyEstimateProgress(22, 20, resetAt)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(20)
			applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 30, resetAt))
			trusted := openAIWeeklyEstimateProgress(21, 40, resetAt)
			applyOpenAIWeeklyEstimateForTest(t, account, trusted)
			requireOpenAIWeeklyEstimate(t, trusted, 1000)

			if tc.mutate != nil {
				tc.mutate(account)
			}
			observed := tc.progress()
			applyOpenAIWeeklyEstimateForTest(t, account, observed)
			requireOpenAIWeeklyEstimatePending(t, observed)
			requireOpenAIWeeklyEstimateV10State(t, account, observed.Utilization, observed.WindowStats.Cost, observed.Utilization, observed.WindowStats.Cost, observed.Utilization)
		})
	}
}

func TestOpenAIWeeklyEstimateAcceptsResetETAJitterWithinAnActiveWindow(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(5)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 30, resetAt))
	first := openAIWeeklyEstimateProgress(21, 40, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, first)
	requireOpenAIWeeklyEstimate(t, first, 1000)

	jittered := openAIWeeklyEstimateProgress(22, 55, resetAt.Add(2*time.Hour))
	applyOpenAIWeeklyEstimateForTest(t, account, jittered)
	requireOpenAIWeeklyEstimate(t, jittered, 1250)
}

func TestOpenAIWeeklyEstimateRebasesLegacyState(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(6)
	resetAt := openAIWeeklyEstimateTestResetAt()
	account.Extra[openAIWeeklyEstimateBaselineKey] = map[string]any{
		"version":            8,
		"segment_percent":    33.0,
		"segment_cost":       699.34,
		"pending_percent":    34.0,
		"pending_max_cost":   700.96,
		"completed_estimate": 2754.0,
		"reset_at":           resetAt.Format(time.RFC3339Nano),
		"identity":           "workspace-a",
	}

	observed := openAIWeeklyEstimateProgress(35, 743.43, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, observed)
	requireOpenAIWeeklyEstimatePending(t, observed)
	requireOpenAIWeeklyEstimateV10State(t, account, 35, 743.43, 35, 743.43, 35)
}

func TestOpenAIWeeklyEstimateAtFullQuotaAlwaysEqualsAccountCost(t *testing.T) {
	t.Parallel()
	resetAt := openAIWeeklyEstimateTestResetAt()
	account := newOpenAIWeeklyEstimateTestAccount(7)
	full := openAIWeeklyEstimateProgress(100, 108.05, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, full)
	requireOpenAIWeeklyEstimate(t, full, 108.05)

	zeroCostAccount := newOpenAIWeeklyEstimateTestAccount(8)
	zeroCost := openAIWeeklyEstimateProgress(100, 0, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, zeroCostAccount, zeroCost)
	requireOpenAIWeeklyEstimate(t, zeroCost, 0)
}

func newOpenAIWeeklyEstimateTestAccount(id int64) *Account {
	return &Account{ID: id, Credentials: map[string]any{"chatgpt_account_id": "workspace-a"}, Extra: map[string]any{}}
}

func openAIWeeklyEstimateTestResetAt() time.Time {
	return time.Date(2099, time.August, 25, 5, 17, 50, 123456789, time.UTC)
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

func requireOpenAIWeeklyEstimateV10State(t *testing.T, account *Account, baselinePercent, baselineCost, observedPercent, observedCost, estimatedPercent float64) {
	t.Helper()
	raw := requireOpenAIWeeklyEstimateRawState(t, account)
	if version := parseExtraInt(raw["version"]); version != openAIWeeklyEstimateStateVersion {
		t.Fatalf("state version = %d, want %d", version, openAIWeeklyEstimateStateVersion)
	}
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "baseline_percent", baselinePercent)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "baseline_cost", baselineCost)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "last_observed_percent", observedPercent)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "last_observed_cost", observedCost)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "last_estimated_percent", estimatedPercent)
}

func requireOpenAIWeeklyEstimateV10HasNoBaseline(t *testing.T, account *Account, observedPercent, observedCost float64) {
	t.Helper()
	raw := requireOpenAIWeeklyEstimateRawState(t, account)
	if version := parseExtraInt(raw["version"]); version != openAIWeeklyEstimateStateVersion {
		t.Fatalf("state version = %d, want %d", version, openAIWeeklyEstimateStateVersion)
	}
	if _, ok := raw["baseline_percent"]; ok {
		t.Fatal("zero-cost nonzero-percent observation must not set a readable baseline")
	}
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "last_observed_percent", observedPercent)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "last_observed_cost", observedCost)
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
