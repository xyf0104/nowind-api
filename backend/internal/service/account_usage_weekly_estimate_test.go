package service

import (
	"math"
	"testing"
	"time"
)

func TestOpenAIWeeklyEstimateUsesClosedPercentageBoundaryMaximum(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(1)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(1, 20, resetAt))
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(2, 35, resetAt))

	// Reaching 3% closes the 2% boundary. Its observed $35 is the first
	// complete 1% endpoint, so the page can now show an estimate.
	atThreePercent := openAIWeeklyEstimateProgress(3, 50, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, atThreePercent)
	requireOpenAIWeeklyEstimate(t, atThreePercent, 1505)

	// 3% remains open until utilization advances. Record its maximum but keep
	// the already completed 2% estimate visible.
	insideThreePercent := openAIWeeklyEstimateProgress(3, 60, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, insideThreePercent)
	requireOpenAIWeeklyEstimate(t, insideThreePercent, 1505)

	// This is the screenshot regression: 3% -> 4% must not become Collecting.
	// The closed 3% maximum is $60, giving ($60 - $35) / 1 = $25 per 1%.
	atFourPercent := openAIWeeklyEstimateProgress(4, 79.70, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, atFourPercent)
	requireOpenAIWeeklyEstimate(t, atFourPercent, 2485)
	requireOpenAIWeeklyEstimateState(t, account, 3, 60, 4, 79.70)

	// More cost at 4% only raises the pending endpoint maximum. It does not
	// revise the completed 3% estimate before 4% has actually closed.
	insideFourPercent := openAIWeeklyEstimateProgress(4, 90, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, insideFourPercent)
	requireOpenAIWeeklyEstimate(t, insideFourPercent, 2485)
	requireOpenAIWeeklyEstimateState(t, account, 3, 60, 4, 90)

	atFivePercent := openAIWeeklyEstimateProgress(5, 120, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, atFivePercent)
	requireOpenAIWeeklyEstimate(t, atFivePercent, 2970)
}

func TestOpenAIWeeklyEstimateImputesMissingPrefixOnlyAfterAClosedLocalInterval(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(2)
	resetAt := openAIWeeklyEstimateTestResetAt()

	// The account was authorized after the upstream window had already reached
	// 20%, but XIASS had no local account cost at that point.
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 0, resetAt))
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 25, resetAt))
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(21, 25, resetAt))
	stillOpen := openAIWeeklyEstimateProgress(21, 30, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, stillOpen)
	requireOpenAIWeeklyEstimatePending(t, stillOpen)

	// 22% closes the maximum $30 at 21%. The first local 1% cost is $30,
	// therefore the missing 20% prefix is fixed once at $600.
	closed := openAIWeeklyEstimateProgress(22, 33, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, closed)
	requireOpenAIWeeklyEstimate(t, closed, 3000)
	raw := requireOpenAIWeeklyEstimateRawState(t, account)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "prefix_percent", 20)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "prefix_estimated_cost", 600)
	if locked, _ := raw["prefix_locked"].(bool); !locked {
		t.Fatal("prefix estimate must be locked after the first complete local interval")
	}
}

func TestOpenAIWeeklyEstimateUsesObservableAverageWhenQuotaJumps(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(3)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(0, 0, resetAt))
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(3, 60, resetAt))

	// No observations exist for 1% or 2%; when 4% arrives, the closed 3%
	// endpoint is the only safe basis and produces the observable 0%-3% average.
	closed := openAIWeeklyEstimateProgress(4, 75, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, closed)
	requireOpenAIWeeklyEstimate(t, closed, 2000)
}

func TestOpenAIWeeklyEstimateRetainsTrustedResultForPureExternalUsage(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(4)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(1, 20, resetAt))
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(2, 40, resetAt))
	trusted := openAIWeeklyEstimateProgress(3, 60, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, trusted)
	requireOpenAIWeeklyEstimate(t, trusted, 2000)

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(4, 80, resetAt))
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(5, 80, resetAt))

	// The 5% point has no XIASS cost increase over the closed 4% endpoint.
	// Rebase sampling without mixing outside-client usage into the rate, while
	// retaining the previously established result instead of showing Collecting.
	external := openAIWeeklyEstimateProgress(6, 80, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, external)
	requireOpenAIWeeklyEstimate(t, external, 2000)
	requireOpenAIWeeklyEstimateState(t, account, 6, 80, 6, 80)
}

func TestOpenAIWeeklyEstimateAcceptsResetETAJitterWithinAnActiveWindow(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(5)
	resetAt := openAIWeeklyEstimateTestResetAt()

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(1, 20, resetAt))
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(2, 40, resetAt))
	trusted := openAIWeeklyEstimateProgress(3, 60, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, trusted)
	requireOpenAIWeeklyEstimate(t, trusted, 2000)

	// The upstream uses a relative remaining duration. A later poll can shift
	// the rendered absolute ETA even though this is still the same active 7d
	// window. The monotonic cost/percentage proof must keep the estimate alive.
	jitteredResetAt := resetAt.Add(2 * time.Hour)
	observed := openAIWeeklyEstimateProgress(4, 80, jitteredResetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, observed)
	requireOpenAIWeeklyEstimate(t, observed, 2000)
}

func TestOpenAIWeeklyEstimateMigratesV7StateWithoutDroppingTheResult(t *testing.T) {
	t.Parallel()
	resetAt := openAIWeeklyEstimateTestResetAt()
	account := newOpenAIWeeklyEstimateTestAccount(6)
	account.Extra[openAIWeeklyEstimateBaselineKey] = map[string]any{
		"version":            7,
		"segment_percent":    2.0,
		"segment_cost":       35.0,
		"last_percent":       3.0,
		"last_cost":          60.0,
		"completed_percent":  3.0,
		"completed_cost":     60.0,
		"completed_estimate": 1505.0,
		"reset_at":           resetAt.Format(time.RFC3339Nano),
		"identity":           "workspace-a",
	}

	observed := openAIWeeklyEstimateProgress(4, 79.70, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, observed)
	requireOpenAIWeeklyEstimate(t, observed, 2485)
	raw := requireOpenAIWeeklyEstimateRawState(t, account)
	if version := parseExtraInt(raw["version"]); version != openAIWeeklyEstimateStateVersion {
		t.Fatalf("state version = %d, want %d", version, openAIWeeklyEstimateStateVersion)
	}
	requireOpenAIWeeklyEstimateState(t, account, 3, 60, 4, 79.70)
}

func TestOpenAIWeeklyEstimateRestartsOnlyForRealStateBreaks(t *testing.T) {
	resetAt := openAIWeeklyEstimateTestResetAt()
	cases := []struct {
		name     string
		mutate   func(*Account)
		progress func() *UsageProgress
	}{
		{
			name: "expired window",
			progress: func() *UsageProgress {
				return openAIWeeklyEstimateProgress(4, 80, time.Now().UTC().Add(-time.Minute))
			},
		},
		{
			name: "new weekly deadline",
			progress: func() *UsageProgress {
				return openAIWeeklyEstimateProgress(4, 80, resetAt.Add(7*24*time.Hour))
			},
		},
		{
			name: "identity change",
			mutate: func(account *Account) {
				account.Credentials["chatgpt_account_id"] = "workspace-b"
			},
			progress: func() *UsageProgress {
				return openAIWeeklyEstimateProgress(4, 80, resetAt)
			},
		},
		{
			name: "reauthorization baseline removal",
			mutate: func(account *Account) {
				delete(account.Extra, openAIWeeklyEstimateBaselineKey)
			},
			progress: func() *UsageProgress {
				return openAIWeeklyEstimateProgress(4, 80, resetAt)
			},
		},
		{
			name: "percent rollback",
			progress: func() *UsageProgress {
				return openAIWeeklyEstimateProgress(2.5, 80, resetAt)
			},
		},
		{
			name: "account cost rollback",
			progress: func() *UsageProgress {
				return openAIWeeklyEstimateProgress(4, 50, resetAt)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(20)
			applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(1, 20, resetAt))
			applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(2, 40, resetAt))
			trusted := openAIWeeklyEstimateProgress(3, 60, resetAt)
			applyOpenAIWeeklyEstimateForTest(t, account, trusted)
			requireOpenAIWeeklyEstimate(t, trusted, 2000)

			if tc.mutate != nil {
				tc.mutate(account)
			}
			observed := tc.progress()
			applyOpenAIWeeklyEstimateForTest(t, account, observed)
			requireOpenAIWeeklyEstimatePending(t, observed)
		})
	}
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

func requireOpenAIWeeklyEstimateState(t *testing.T, account *Account, segmentPercent, segmentCost, pendingPercent, pendingMaxCost float64) {
	t.Helper()
	raw := requireOpenAIWeeklyEstimateRawState(t, account)
	if version := parseExtraInt(raw["version"]); version != openAIWeeklyEstimateStateVersion {
		t.Fatalf("state version = %d, want %d", version, openAIWeeklyEstimateStateVersion)
	}
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "segment_percent", segmentPercent)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "segment_cost", segmentCost)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "pending_percent", pendingPercent)
	requireOpenAIWeeklyEstimateStateNumber(t, raw, "pending_max_cost", pendingMaxCost)
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
