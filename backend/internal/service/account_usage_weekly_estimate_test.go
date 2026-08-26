package service

import (
	"math"
	"testing"
	"time"
)

func TestOpenAIWeeklyFrozenEstimateUsesPreviousPercentDenominator(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(1)
	resetAt := openAIWeeklyEstimateTestResetAt()
	firstAt := time.Date(2026, time.August, 26, 8, 0, 0, 0, time.UTC)

	atEight := openAIWeeklyEstimateProgress(8, 213.83, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atEight, 213.83, 213.83, true, firstAt)
	requireOpenAIWeeklyEstimate(t, atEight, 213.83*100/7)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 8, 213.83)

	// The local account cost can grow while OpenAI continues to report 8%.
	// Rule B keeps the value frozen until the next integer percentage appears.
	stillEight := openAIWeeklyEstimateProgress(8.99, 239.99, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, stillEight, 239.99, 239.99, true, firstAt.Add(time.Minute))
	requireOpenAIWeeklyEstimate(t, stillEight, 213.83*100/7)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 8, 213.83)

	atNine := openAIWeeklyEstimateProgress(9, 240, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atNine, 240, 240, true, firstAt.Add(2*time.Minute))
	requireOpenAIWeeklyEstimate(t, atNine, 3000)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 9, 240)

	atTen := openAIWeeklyEstimateProgress(10, 260, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atTen, 260, 260, true, firstAt.Add(3*time.Minute))
	requireOpenAIWeeklyEstimate(t, atTen, 260/0.09)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 10, 260)
}

func TestOpenAIWeeklyFrozenEstimatePairsCostWithQuotaObservationTime(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(2)
	resetAt := openAIWeeklyEstimateTestResetAt()
	observedAt := time.Date(2026, time.August, 26, 8, 15, 0, 0, time.UTC)

	// The live total can advance after the official 9% observation. The frozen
	// number must keep the $240 that existed at that observation, not $999.
	progress := openAIWeeklyEstimateProgress(9, 999, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, 240, 999, true, observedAt)
	requireOpenAIWeeklyEstimate(t, progress, 3000)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 9, 240)
}

func TestOpenAIWeeklyFrozenEstimateWaitsUntilThereIsAPreviousPercent(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(3)
	resetAt := openAIWeeklyEstimateTestResetAt()
	observedAt := time.Date(2026, time.August, 26, 8, 30, 0, 0, time.UTC)

	atOne := openAIWeeklyEstimateProgress(1, 30, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atOne, 30, 30, true, observedAt)
	requireOpenAIWeeklyEstimatePending(t, atOne)

	atTwo := openAIWeeklyEstimateProgress(2, 60, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atTwo, 60, 60, true, observedAt.Add(time.Minute))
	requireOpenAIWeeklyEstimate(t, atTwo, 6000)
}

func TestOpenAIWeeklyFrozenEstimateRetainsLastValueWhenNextSnapshotCannotBeAligned(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(4)
	resetAt := openAIWeeklyEstimateTestResetAt()
	observedAt := time.Date(2026, time.August, 26, 8, 45, 0, 0, time.UTC)

	atTen := openAIWeeklyEstimateProgress(10, 260, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atTen, 260, 260, true, observedAt)
	requireOpenAIWeeklyEstimate(t, atTen, 260.0*100/9)

	// The provider has advanced, but no point-in-time local cost is available
	// yet. Keep the prior frozen value rather than computing from a later total.
	atEleven := openAIWeeklyEstimateProgress(11, 320, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atEleven, 0, 320, false, observedAt.Add(time.Minute))
	requireOpenAIWeeklyEstimate(t, atEleven, 260.0*100/9)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 10, 260)
}

func TestOpenAIWeeklyFrozenEstimatePreservesSameAccountAcrossCredentialRotation(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(5)
	resetAt := openAIWeeklyEstimateTestResetAt()
	observedAt := time.Date(2026, time.August, 26, 9, 0, 0, 0, time.UTC)

	atTen := openAIWeeklyEstimateProgress(10, 260, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atTen, 260, 260, true, observedAt)
	requireOpenAIWeeklyEstimate(t, atTen, 260.0*100/9)

	// A 401 reauthorization rotates tokens but keeps the verified ChatGPT
	// account identity, so the existing percentage snapshot stays intact.
	account.Credentials["access_token"] = "access-rotated"
	account.Credentials["refresh_token"] = "refresh-rotated"
	stillTen := openAIWeeklyEstimateProgress(10, 400, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, stillTen, 400, 400, true, observedAt.Add(time.Minute))
	requireOpenAIWeeklyEstimate(t, stillTen, 260.0*100/9)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 10, 260)
}

func TestOpenAIWeeklyFrozenEstimateRebuildsForNewIdentityWindowOrPercentRegression(t *testing.T) {
	resetAt := openAIWeeklyEstimateTestResetAt()
	observedAt := time.Date(2026, time.August, 26, 9, 15, 0, 0, time.UTC)

	cases := []struct {
		name    string
		mutate  func(*Account)
		resetAt time.Time
		percent float64
		cost    float64
		want    float64
		bucket  int
	}{
		{
			name: "new identity",
			mutate: func(account *Account) {
				account.Credentials["chatgpt_account_id"] = "workspace-b"
			},
			resetAt: resetAt,
			percent: 10,
			cost:    100,
			want:    100.0 * 100 / 9,
			bucket:  10,
		},
		{
			name:    "new weekly window",
			resetAt: resetAt.Add(8 * 24 * time.Hour),
			percent: 10,
			cost:    100,
			want:    100.0 * 100 / 9,
			bucket:  10,
		},
		{
			name:    "provider percent regression",
			resetAt: resetAt,
			percent: 9,
			cost:    240,
			want:    3000,
			bucket:  9,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(6)
			atTen := openAIWeeklyEstimateProgress(10, 260, resetAt)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, atTen, 260, 260, true, observedAt)
			requireOpenAIWeeklyEstimate(t, atTen, 260.0*100/9)

			if tc.mutate != nil {
				tc.mutate(account)
			}
			current := openAIWeeklyEstimateProgress(tc.percent, tc.cost, tc.resetAt)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, current, tc.cost, tc.cost, true, observedAt.Add(time.Minute))
			requireOpenAIWeeklyEstimate(t, current, tc.want)
			requireOpenAIWeeklyFrozenEstimateState(t, account, tc.bucket, tc.cost)
		})
	}
}

func TestOpenAIWeeklyFrozenEstimateAtFullQuotaUsesCurrentAccountCost(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(7)
	progress := openAIWeeklyEstimateProgress(100, 108.05, openAIWeeklyEstimateTestResetAt())

	applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, 99, 108.05, false, time.Now().UTC())
	requireOpenAIWeeklyEstimate(t, progress, 108.05)
}

func TestOpenAIWeeklyFrozenEstimateReplacesLegacySlopeState(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(8)
	resetAt := openAIWeeklyEstimateTestResetAt()
	account.Extra[openAIWeeklyEstimateBaselineKey] = map[string]any{
		"version":            12,
		"completed_estimate": 520.0,
	}

	progress := openAIWeeklyEstimateProgress(8, 213.83, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, 213.83, 213.83, true, time.Now().UTC())
	requireOpenAIWeeklyEstimate(t, progress, 213.83*100/7)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 8, 213.83)
}

func TestOpenAICodexSnapshotObservationAtAllowsRequestDurationDrift(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 26, 8, 0, 0, 0, time.UTC)
	account := newOpenAIWeeklyEstimateTestAccount(9)
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

func requireOpenAIWeeklyFrozenEstimateState(t *testing.T, account *Account, wantBucket int, wantCost float64) {
	t.Helper()
	state, ok := readOpenAIWeeklyFrozenEstimateState(account.Extra)
	if !ok {
		t.Fatalf("weekly frozen estimate state is missing or invalid: %#v", account.Extra[openAIWeeklyEstimateBaselineKey])
	}
	if state.PercentBucket != wantBucket {
		t.Fatalf("frozen bucket = %d, want %d", state.PercentBucket, wantBucket)
	}
	if math.Abs(state.SnapshotCost-wantCost) > 1e-9 {
		t.Fatalf("frozen cost = %v, want %v", state.SnapshotCost, wantCost)
	}
}
