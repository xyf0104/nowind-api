package service

import (
	"math"
	"testing"
	"time"
)

func TestOpenAIWeeklyEstimateUsesNewestCompletedPercentInterval(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(1)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 30, resetAt))
	first := openAIWeeklyEstimateProgress(21, 40, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, first)
	requireOpenAIWeeklyEstimate(t, first, 40+79*10)

	// At the same endpoint, use its current maximum account cost immediately.
	samePercent := openAIWeeklyEstimateProgress(21, 50, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, samePercent)
	requireOpenAIWeeklyEstimate(t, samePercent, 50+79*20)

	// An unfinished next interval keeps the previous complete estimate.
	partial := openAIWeeklyEstimateProgress(21.5, 55, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, partial)
	requireOpenAIWeeklyEstimate(t, partial, 50+79*20)

	// The next full interval uses only 21% -> 22%, never a historical average.
	next := openAIWeeklyEstimateProgress(22, 60, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, next)
	requireOpenAIWeeklyEstimate(t, next, 60+78*10)
	requireOpenAIWeeklyEstimateStateVersion(t, account)
}

func TestOpenAIWeeklyEstimateUsesActualSkippedIntervalAverage(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(2)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 30, resetAt))
	skipped := openAIWeeklyEstimateProgress(22, 55, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, skipped)

	// No intermediate observation exists, so $25 / 2 percentage points is the
	// only valid rate for this latest complete interval.
	requireOpenAIWeeklyEstimate(t, skipped, 55+78*(25.0/2.0))
}

func TestOpenAIWeeklyEstimateDoesNotSmoothAgainstHistoricIntervals(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(3)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(33, 699.34, resetAt))
	atThirtyFour := openAIWeeklyEstimateProgress(34, 700.96, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, atThirtyFour)
	requireOpenAIWeeklyEstimate(t, atThirtyFour, 700.96+66*(700.96-699.34))

	atThirtyFive := openAIWeeklyEstimateProgress(35, 743.43, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, atThirtyFive)
	requireOpenAIWeeklyEstimate(t, atThirtyFive, 743.43+65*(743.43-700.96))
}

func TestOpenAIWeeklyEstimateRetainsLastResultAcrossUnbilledExternalAdvance(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(4)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 30, resetAt))
	trusted := openAIWeeklyEstimateProgress(21, 40, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, trusted)
	requireOpenAIWeeklyEstimate(t, trusted, 830)

	// The provider percentage moved but no XIASS account cost was added. Rebase
	// without blending that external use into the next local interval.
	external := openAIWeeklyEstimateProgress(22, 40, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, external)
	requireOpenAIWeeklyEstimate(t, external, 830)

	localAgain := openAIWeeklyEstimateProgress(23, 50, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, localAgain)
	requireOpenAIWeeklyEstimate(t, localAgain, 50+77*10)
}

func TestOpenAIWeeklyEstimateCompensatesMidWindowZeroCostLogin(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(5)
	resetAt := openAIWeeklyEstimateTestResetAt()

	// This account was added at 20% before XIASS had recorded any local cost.
	initial := openAIWeeklyEstimateProgress(20, 0, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, initial)
	requireOpenAIWeeklyEstimatePending(t, initial)

	// Cost collected at the unchanged endpoint is the first interval's start.
	stillTwenty := openAIWeeklyEstimateProgress(20, 30, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, stillTwenty)
	requireOpenAIWeeklyEstimatePending(t, stillTwenty)

	firstComplete := openAIWeeklyEstimateProgress(21, 40, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, firstComplete)
	// $10 per point compensates the prior 20%, then adds the real local total
	// and the unconsumed 79%.
	requireOpenAIWeeklyEstimate(t, firstComplete, 20*10+40+79*10)

	// A higher maximum at 21% must update both that interval and its bootstrap.
	sameEndpoint := openAIWeeklyEstimateProgress(21, 50, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, sameEndpoint)
	requireOpenAIWeeklyEstimate(t, sameEndpoint, 20*20+50+79*20)

	// Once OpenAI moves on, the bootstrap is fixed and only the most recent
	// 1% interval controls the remaining projection.
	nextComplete := openAIWeeklyEstimateProgress(22, 55, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, nextComplete)
	requireOpenAIWeeklyEstimate(t, nextComplete, 20*20+55+78*5)
}

func TestOpenAIWeeklyEstimateKeepsStateAcrossCredentialRotation(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(6)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 30, resetAt))
	first := openAIWeeklyEstimateProgress(21, 40, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, first)
	requireOpenAIWeeklyEstimate(t, first, 830)

	// Routine token rotation preserves the same ChatGPT identity and therefore
	// must not erase the sampled interval. Interactive reauthorization clears
	// this state at its dedicated credential-apply boundary.
	account.Credentials["access_token"] = "rotated-access-token"
	account.Credentials["refresh_token"] = "rotated-refresh-token"
	afterRotation := openAIWeeklyEstimateProgress(22, 55, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, afterRotation)
	requireOpenAIWeeklyEstimate(t, afterRotation, 55+78*15)
}

func TestOpenAIWeeklyEstimateResetsOnIdentityWindowPercentOrCostRegression(t *testing.T) {
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
			requireOpenAIWeeklyEstimate(t, trusted, 830)

			if tc.mutate != nil {
				tc.mutate(account)
			}
			observed := tc.progress()
			applyOpenAIWeeklyEstimateForTest(t, account, observed)
			requireOpenAIWeeklyEstimatePending(t, observed)
		})
	}
}

func TestOpenAIWeeklyEstimateRebuildsLegacyCumulativeState(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(7)
	resetAt := openAIWeeklyEstimateTestResetAt()
	account.Extra[openAIWeeklyEstimateBaselineKey] = map[string]any{
		"version":            10,
		"baseline_percent":   33.0,
		"baseline_cost":      699.34,
		"completed_estimate": 2754.0,
		"reset_at":           resetAt.Format(time.RFC3339Nano),
		"identity":           "workspace-a",
	}

	observed := openAIWeeklyEstimateProgress(35, 743.43, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, observed)
	requireOpenAIWeeklyEstimatePending(t, observed)
	requireOpenAIWeeklyEstimateStateVersion(t, account)
}

func TestOpenAIWeeklyEstimateAtFullQuotaAlwaysEqualsAccountCost(t *testing.T) {
	t.Parallel()
	resetAt := openAIWeeklyEstimateTestResetAt()
	account := newOpenAIWeeklyEstimateTestAccount(8)
	full := openAIWeeklyEstimateProgress(100, 108.05, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, full)
	requireOpenAIWeeklyEstimate(t, full, 108.05)

	zeroCostAccount := newOpenAIWeeklyEstimateTestAccount(9)
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

func requireOpenAIWeeklyEstimateStateVersion(t *testing.T, account *Account) {
	t.Helper()
	raw, ok := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if !ok {
		t.Fatal("weekly estimate state is missing")
	}
	if version := parseExtraInt(raw["version"]); version != openAIWeeklyEstimateStateVersion {
		t.Fatalf("state version = %d, want %d", version, openAIWeeklyEstimateStateVersion)
	}
}
