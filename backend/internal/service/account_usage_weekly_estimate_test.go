package service

import (
	"math"
	"testing"
	"time"
)

func TestOpenAIWeeklyFrozenEstimateUsesJoinBaselineAverage(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(1)
	resetAt := openAIWeeklyEstimateTestResetAt()
	firstAt := time.Date(2026, time.August, 26, 8, 0, 0, 0, time.UTC)

	joined := openAIWeeklyEstimateProgress(20, 0, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, joined, 0, 0, true, firstAt)
	requireOpenAIWeeklyEstimatePending(t, joined)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 20, 0, 20, 0)

	atTwentyOne := openAIWeeklyEstimateProgress(21, 20, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atTwentyOne, 20, 20, true, firstAt.Add(time.Minute))
	requireOpenAIWeeklyEstimate(t, atTwentyOne, 2000)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 20, 0, 21, 20)

	// The provider can continue reporting 21% while XIASS cost grows. The
	// largest aligned cost in that bucket must replace the earlier value.
	stillTwentyOne := openAIWeeklyEstimateProgress(21.9, 30, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, stillTwentyOne, 30, 30, true, firstAt.Add(2*time.Minute))
	requireOpenAIWeeklyEstimate(t, stillTwentyOne, 3000)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 20, 0, 21, 30)

	staleTwentyOne := openAIWeeklyEstimateProgress(21.9, 25, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, staleTwentyOne, 25, 25, true, firstAt.Add(3*time.Minute))
	requireOpenAIWeeklyEstimate(t, staleTwentyOne, 3000)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 20, 0, 21, 30)

	// Keep averaging from the join baseline, not from the latest one-percent
	// interval: $120 / (25 - 20) * 100 = $2400.
	atTwentyFive := openAIWeeklyEstimateProgress(25, 120, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atTwentyFive, 120, 120, true, firstAt.Add(4*time.Minute))
	requireOpenAIWeeklyEstimate(t, atTwentyFive, 2400)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 20, 0, 25, 120)
}

func TestOpenAIWeeklyFrozenEstimateFirstMidWindowObservationStaysPending(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(2)
	resetAt := openAIWeeklyEstimateTestResetAt()
	observedAt := time.Date(2026, time.August, 26, 8, 15, 0, 0, time.UTC)

	// This reproduces the UI case that incorrectly showed about $709.31 by
	// dividing the first $127.93 XIASS total by the provider's previous 18%.
	progress := openAIWeeklyEstimateProgress(19, 127.93, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, 127.93, 127.93, true, observedAt)
	requireOpenAIWeeklyEstimatePending(t, progress)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 19, 127.93, 19, 127.93)
}

func TestOpenAIWeeklyFrozenEstimatePairsCostWithQuotaObservationTime(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(3)
	resetAt := openAIWeeklyEstimateTestResetAt()
	firstAt := time.Date(2026, time.August, 26, 8, 30, 0, 0, time.UTC)

	baseline := openAIWeeklyEstimateProgress(8, 200, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, baseline, 200, 200, true, firstAt)
	requireOpenAIWeeklyEstimatePending(t, baseline)

	// The live total advanced to $999 after the official 9% observation. The
	// estimate must use the bounded $240 snapshot paired with that observation.
	progress := openAIWeeklyEstimateProgress(9, 999, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, 240, 999, true, firstAt.Add(time.Minute))
	requireOpenAIWeeklyEstimate(t, progress, 4000)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 8, 200, 9, 240)
}

func TestOpenAIWeeklyFrozenEstimateRetainsLastValueWhenNextSnapshotCannotBeAligned(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(4)
	resetAt := openAIWeeklyEstimateTestResetAt()
	observedAt := time.Date(2026, time.August, 26, 8, 45, 0, 0, time.UTC)

	baseline := openAIWeeklyEstimateProgress(10, 260, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, baseline, 260, 260, true, observedAt)
	atEleven := openAIWeeklyEstimateProgress(11, 320, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atEleven, 320, 320, true, observedAt.Add(time.Minute))
	requireOpenAIWeeklyEstimate(t, atEleven, 6000)

	atTwelve := openAIWeeklyEstimateProgress(12, 400, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atTwelve, 0, 400, false, observedAt.Add(2*time.Minute))
	requireOpenAIWeeklyEstimate(t, atTwelve, 6000)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 10, 260, 11, 320)
}

func TestOpenAIWeeklyFrozenEstimateRebasesWhenProviderAdvancesWithoutLocalCost(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(5)
	resetAt := openAIWeeklyEstimateTestResetAt()
	observedAt := time.Date(2026, time.August, 26, 9, 0, 0, 0, time.UTC)

	baseline := openAIWeeklyEstimateProgress(20, 0, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, baseline, 0, 0, true, observedAt)
	externalAdvance := openAIWeeklyEstimateProgress(21, 0, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, externalAdvance, 0, 0, true, observedAt.Add(time.Minute))
	requireOpenAIWeeklyEstimatePending(t, externalAdvance)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 21, 0, 21, 0)

	localAdvance := openAIWeeklyEstimateProgress(22, 20, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, localAdvance, 20, 20, true, observedAt.Add(2*time.Minute))
	requireOpenAIWeeklyEstimate(t, localAdvance, 2000)
}

func TestOpenAIWeeklyFrozenEstimatePreservesSameAccountAcrossCredentialRotation(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(6)
	resetAt := openAIWeeklyEstimateTestResetAt()
	observedAt := time.Date(2026, time.August, 26, 9, 15, 0, 0, time.UTC)

	baseline := openAIWeeklyEstimateProgress(10, 260, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, baseline, 260, 260, true, observedAt)
	atEleven := openAIWeeklyEstimateProgress(11, 280, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atEleven, 280, 280, true, observedAt.Add(time.Minute))
	requireOpenAIWeeklyEstimate(t, atEleven, 2000)

	account.Credentials["access_token"] = "access-rotated"
	account.Credentials["refresh_token"] = "refresh-rotated"
	stillEleven := openAIWeeklyEstimateProgress(11, 290, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, stillEleven, 290, 290, true, observedAt.Add(2*time.Minute))
	requireOpenAIWeeklyEstimate(t, stillEleven, 3000)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 10, 260, 11, 290)
}

func TestOpenAIWeeklyFrozenEstimateRebuildsForNewIdentityWindowOrRegression(t *testing.T) {
	resetAt := openAIWeeklyEstimateTestResetAt()
	observedAt := time.Date(2026, time.August, 26, 9, 30, 0, 0, time.UTC)

	cases := []struct {
		name    string
		mutate  func(*Account)
		resetAt time.Time
		percent float64
		cost    float64
	}{
		{
			name: "new identity",
			mutate: func(account *Account) {
				account.Credentials["chatgpt_account_id"] = "workspace-b"
			},
			resetAt: resetAt,
			percent: 10,
			cost:    100,
		},
		{
			name:    "new weekly window",
			resetAt: resetAt.Add(8 * 24 * time.Hour),
			percent: 10,
			cost:    100,
		},
		{
			name:    "provider percent regression",
			resetAt: resetAt,
			percent: 9,
			cost:    240,
		},
		{
			name:    "XIASS cost regression",
			resetAt: resetAt,
			percent: 12,
			cost:    200,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(7)
			baseline := openAIWeeklyEstimateProgress(10, 260, resetAt)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, baseline, 260, 260, true, observedAt)
			atEleven := openAIWeeklyEstimateProgress(11, 280, resetAt)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, atEleven, 280, 280, true, observedAt.Add(time.Minute))
			requireOpenAIWeeklyEstimate(t, atEleven, 2000)

			if tc.mutate != nil {
				tc.mutate(account)
			}
			current := openAIWeeklyEstimateProgress(tc.percent, tc.cost, tc.resetAt)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, current, tc.cost, tc.cost, true, observedAt.Add(2*time.Minute))
			requireOpenAIWeeklyEstimatePending(t, current)
			requireOpenAIWeeklyFrozenEstimateState(t, account, int(tc.percent), tc.cost, int(tc.percent), tc.cost)
		})
	}
}

func TestOpenAIWeeklyFrozenEstimateAtFullQuotaUsesCurrentAccountCost(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(8)
	progress := openAIWeeklyEstimateProgress(100, 108.05, openAIWeeklyEstimateTestResetAt())

	applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, 99, 108.05, false, time.Now().UTC())
	requireOpenAIWeeklyEstimate(t, progress, 108.05)
}

func TestOpenAIWeeklyFrozenEstimateMigratesLegacyObservationWithoutLegacyEstimate(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(9)
	resetAt := openAIWeeklyEstimateTestResetAt()
	observedAt := time.Date(2026, time.August, 26, 10, 0, 0, 0, time.UTC)
	account.Extra[openAIWeeklyEstimateBaselineKey] = map[string]any{
		"version":             13,
		"percent_bucket":      19,
		"snapshot_cost":       127.93,
		"has_weekly_estimate": true,
		"estimate_usd":        709.31,
		"reset_at":            resetAt.Format(time.RFC3339Nano),
		"identity":            "workspace-a",
		"observed_at":         observedAt.Format(time.RFC3339Nano),
	}

	current := openAIWeeklyEstimateProgress(19, 127.93, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, current, 127.93, 127.93, true, observedAt)
	requireOpenAIWeeklyEstimatePending(t, current)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 19, 127.93, 19, 127.93)

	raw := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if got := parseExtraInt(raw["version"]); got != openAIWeeklyFrozenEstimateStateVersion {
		t.Fatalf("migrated state version = %d, want %d", got, openAIWeeklyFrozenEstimateStateVersion)
	}
	if _, exists := raw["estimate_usd"]; exists {
		t.Fatalf("legacy estimate survived migration: %#v", raw)
	}

	next := openAIWeeklyEstimateProgress(20, 147.93, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, next, 147.93, 147.93, true, observedAt.Add(time.Minute))
	requireOpenAIWeeklyEstimate(t, next, 2000)
}

func TestOpenAICodexSnapshotObservationAtAllowsRequestDurationDrift(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 26, 8, 0, 0, 0, time.UTC)
	account := newOpenAIWeeklyEstimateTestAccount(10)
	account.Extra["codex_usage_updated_at"] = now.Add(2 * time.Second).Format(time.RFC3339Nano)

	observedAt, ok := openAICodexSnapshotObservationAt(account, now)
	wantObservedAt := now.Add(2 * time.Second)
	if !ok || !observedAt.Equal(wantObservedAt) {
		t.Fatalf("small future timestamp = (%v, %v), want (%v, true)", observedAt, ok, wantObservedAt)
	}

	account.Extra["codex_usage_updated_at"] = now.Add(6 * time.Second).Format(time.RFC3339Nano)
	if observedAt, ok := openAICodexSnapshotObservationAt(account, now); ok || !observedAt.IsZero() {
		t.Fatalf("large future timestamp = (%v, %v), want (zero, false)", observedAt, ok)
	}
}

func newOpenAIWeeklyEstimateTestAccount(id int64) *Account {
	return &Account{
		ID: id,
		Credentials: map[string]any{
			"chatgpt_account_id": "workspace-a",
		},
		Extra: map[string]any{},
	}
}

func openAIWeeklyEstimateTestResetAt() time.Time {
	return time.Date(2099, time.August, 25, 5, 17, 50, 123456789, time.UTC)
}

func applyOpenAIWeeklyFrozenEstimateForTest(
	t *testing.T,
	account *Account,
	progress *UsageProgress,
	snapshotCost, currentCost float64,
	matched bool,
	observedAt time.Time,
) {
	t.Helper()
	estimate, updates := calculateOpenAIWeeklyFrozenEstimate(
		account,
		progress,
		snapshotCost,
		currentCost,
		matched,
		observedAt,
	)
	progress.WeeklyEstimateUSD = estimate
	if len(updates) > 0 {
		mergeAccountExtra(account, updates)
	}
}

func openAIWeeklyEstimateProgress(percent, cost float64, resetAt time.Time) *UsageProgress {
	return &UsageProgress{
		Utilization: percent,
		ResetsAt:    &resetAt,
		WindowStats: &WindowStats{Cost: cost},
	}
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

func requireOpenAIWeeklyFrozenEstimateState(
	t *testing.T,
	account *Account,
	wantBaselinePercent int,
	wantBaselineCost float64,
	wantBucket int,
	wantCost float64,
) {
	t.Helper()
	state, ok := readOpenAIWeeklyFrozenEstimateState(account.Extra)
	if !ok {
		t.Fatalf("weekly frozen estimate state is missing or invalid: %#v", account.Extra[openAIWeeklyEstimateBaselineKey])
	}
	if state.BaselinePercent != wantBaselinePercent {
		t.Fatalf("baseline percent = %d, want %d", state.BaselinePercent, wantBaselinePercent)
	}
	if math.Abs(state.BaselineCost-wantBaselineCost) > 1e-9 {
		t.Fatalf("baseline cost = %v, want %v", state.BaselineCost, wantBaselineCost)
	}
	if state.PercentBucket != wantBucket {
		t.Fatalf("current bucket = %d, want %d", state.PercentBucket, wantBucket)
	}
	if math.Abs(state.SnapshotCost-wantCost) > 1e-9 {
		t.Fatalf("current cost = %v, want %v", state.SnapshotCost, wantCost)
	}
}
