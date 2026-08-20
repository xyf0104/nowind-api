package service

import (
	"math"
	"testing"
	"time"
)

func TestOpenAIWeeklyEstimateHoldsTrustedZeroBaselineUntilNextCompletedPercent(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          1,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra:       map[string]any{},
	}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)

	initial := openAIWeeklyEstimateProgress(0, 0, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, initial)
	requireOpenAIWeeklyEstimatePending(t, initial)

	first := openAIWeeklyEstimateProgress(6, 176, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, first)
	requireOpenAIWeeklyEstimate(t, first, 176/0.06)

	// Cost can continue accumulating inside the current percentage. It belongs
	// to the next completed checkpoint and must not move the display yet.
	samePercent := openAIWeeklyEstimateProgress(6, 178, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, samePercent)
	requireOpenAIWeeklyEstimate(t, samePercent, 176/0.06)

	// Once another full percentage completes, replace the display with the
	// cumulative average across all seven observed points.
	nextPercent := openAIWeeklyEstimateProgress(7, 190, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, nextPercent)
	requireOpenAIWeeklyEstimate(t, nextPercent, 190/0.07)
}

func TestOpenAIWeeklyEstimateUsesCumulativeAverageFromLoginBaseline(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          13,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra:       map[string]any{},
	}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(4, 20, resetAt))
	first := openAIWeeklyEstimateProgress(5, 50, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, first)
	requireOpenAIWeeklyEstimate(t, first, 2900)

	withinPercent := openAIWeeklyEstimateProgress(5, 60, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, withinPercent)
	requireOpenAIWeeklyEstimate(t, withinPercent, 2900)

	second := openAIWeeklyEstimateProgress(6, 90, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, second)
	requireOpenAIWeeklyEstimate(t, second, 3380)
}

func TestOpenAIWeeklyEstimateUsesCostAtPercentageBoundary(t *testing.T) {
	t.Parallel()

	account := &Account{ID: 14, Extra: map[string]any{}}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(0, 0, resetAt))

	percentEightStarted := openAIWeeklyEstimateProgress(8, 220, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, percentEightStarted)
	requireOpenAIWeeklyEstimate(t, percentEightStarted, 220+(220.0/8)*92)

	percentEightInProgress := openAIWeeklyEstimateProgress(8, 225.45, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, percentEightInProgress)
	requireOpenAIWeeklyEstimate(t, percentEightInProgress, 220+(220.0/8)*92)
	requireOpenAIWeeklyEstimateCheckpoint(t, account, 8, 220)

	percentEightCompleted := openAIWeeklyEstimateProgress(9, 258, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, percentEightCompleted)
	requireOpenAIWeeklyEstimate(t, percentEightCompleted, 258+(258.0/9)*91)
	requireOpenAIWeeklyEstimateCheckpoint(t, account, 9, 258)
}

func TestOpenAIWeeklyEstimateValuesUnbilledLoginPrefixAtObservedRate(t *testing.T) {
	t.Parallel()

	account := &Account{ID: 15, Extra: map[string]any{}}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 0, resetAt))

	progress := openAIWeeklyEstimateProgress(21, 30, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, progress)
	requireOpenAIWeeklyEstimate(t, progress, 3000)
}

func TestOpenAIWeeklyEstimateUsesLoginDeltaForNonZeroBaseline(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          2,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra:       map[string]any{},
	}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(26, 100, resetAt))
	incomplete := openAIWeeklyEstimateProgress(26.9, 145, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, incomplete)
	requireOpenAIWeeklyEstimatePending(t, incomplete)

	complete := openAIWeeklyEstimateProgress(27, 150, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, complete)
	requireOpenAIWeeklyEstimate(t, complete, 3800)

	samePercent := openAIWeeklyEstimateProgress(27, 160, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, samePercent)
	requireOpenAIWeeklyEstimate(t, samePercent, 3800)

	secondComplete := openAIWeeklyEstimateProgress(28, 190, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, secondComplete)
	requireOpenAIWeeklyEstimate(t, secondComplete, 3430)
}

func TestOpenAIWeeklyEstimateAveragesVariableCostsAcrossTrustedWindow(t *testing.T) {
	t.Parallel()

	account := &Account{ID: 3, Extra: map[string]any{}}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(0, 0, resetAt))

	first := openAIWeeklyEstimateProgress(1, 45, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, first)
	requireOpenAIWeeklyEstimate(t, first, 4500)

	second := openAIWeeklyEstimateProgress(2, 75, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, second)
	requireOpenAIWeeklyEstimate(t, second, 3750)
}

func TestOpenAIWeeklyEstimateAveragesMultiPointJump(t *testing.T) {
	t.Parallel()

	account := &Account{ID: 4, Extra: map[string]any{}}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(40, 200, resetAt))

	progress := openAIWeeklyEstimateProgress(43, 320, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, progress)
	requireOpenAIWeeklyEstimate(t, progress, 2600)
}

func TestOpenAIWeeklyEstimateRebasesUnbilledExternalUsage(t *testing.T) {
	t.Parallel()

	account := &Account{ID: 5, Extra: map[string]any{}}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 100, resetAt))

	externalOnly := openAIWeeklyEstimateProgress(22, 100, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, externalOnly)
	requireOpenAIWeeklyEstimatePending(t, externalOnly)

	samePercentCost := openAIWeeklyEstimateProgress(22, 130, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, samePercentCost)
	requireOpenAIWeeklyEstimatePending(t, samePercentCost)

	localSegment := openAIWeeklyEstimateProgress(23, 140, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, localSegment)
	requireOpenAIWeeklyEstimate(t, localSegment, 3220)
}

func TestOpenAIWeeklyEstimateRestartsForWindowIdentityOrCostRollback(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          6,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra:       map[string]any{},
	}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(50, 1000, resetAt))
	withEstimate := openAIWeeklyEstimateProgress(51, 1060, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, withEstimate)
	requireOpenAIWeeklyEstimate(t, withEstimate, 4000)

	newWindowReset := resetAt.Add(7 * 24 * time.Hour)
	newWindow := openAIWeeklyEstimateProgress(12, 50, newWindowReset)
	applyOpenAIWeeklyEstimateForTest(t, account, newWindow)
	requireOpenAIWeeklyEstimatePending(t, newWindow)

	afterWindow := openAIWeeklyEstimateProgress(13, 90, newWindowReset)
	applyOpenAIWeeklyEstimateForTest(t, account, afterWindow)
	requireOpenAIWeeklyEstimate(t, afterWindow, 3570)

	account.Credentials["chatgpt_account_id"] = "workspace-b"
	newIdentity := openAIWeeklyEstimateProgress(14, 130, newWindowReset)
	applyOpenAIWeeklyEstimateForTest(t, account, newIdentity)
	requireOpenAIWeeklyEstimatePending(t, newIdentity)

	rollback := openAIWeeklyEstimateProgress(15, 120, newWindowReset)
	applyOpenAIWeeklyEstimateForTest(t, account, rollback)
	requireOpenAIWeeklyEstimatePending(t, rollback)
}

func TestOpenAIWeeklyEstimateMigratesV3IntoLiveCumulativeBaseline(t *testing.T) {
	t.Parallel()

	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	account := &Account{
		ID:          7,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra: map[string]any{
			openAIWeeklyEstimateBaselineKey: map[string]any{
				"version":              3,
				"segment_percent":      6.0,
				"segment_cost":         176.0,
				"last_percent":         6.0,
				"last_cost":            176.0,
				"recent_rates":         []any{45.0, 25.0, 18.0},
				"completed_estimate":   2449.0,
				"segment_max_estimate": 2449.0,
				"reset_at":             resetAt.Format(time.RFC3339Nano),
				"identity":             "workspace-a",
			},
		},
	}

	progress := openAIWeeklyEstimateProgress(6, 178, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, progress)
	requireOpenAIWeeklyEstimate(t, progress, 176/0.06)

	raw := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if got := parseExtraInt(raw["version"]); got != openAIWeeklyEstimateStateVersion {
		t.Fatalf("state version = %d, want %d", got, openAIWeeklyEstimateStateVersion)
	}
	if got, _ := parseOpenAIWeeklyEstimateNumber(raw, "anchor_percent"); got != 0 {
		t.Fatalf("anchor percent = %v, want 0", got)
	}
	if got, _ := parseOpenAIWeeklyEstimateNumber(raw, "anchor_cost"); math.Abs(got) > 1e-9 {
		t.Fatalf("anchor cost = %v, want 0", got)
	}
}

func TestOpenAIWeeklyEstimateMigratesEarlyV3StateWithoutRateHistory(t *testing.T) {
	t.Parallel()

	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	account := &Account{
		ID:          12,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra: map[string]any{
			openAIWeeklyEstimateBaselineKey: map[string]any{
				"version":            3,
				"segment_percent":    6.0,
				"segment_cost":       176.0,
				"last_percent":       6.0,
				"last_cost":          176.0,
				"completed_estimate": 2449.0,
				"reset_at":           resetAt.Format(time.RFC3339Nano),
				"identity":           "workspace-a",
			},
		},
	}

	progress := openAIWeeklyEstimateProgress(6, 178, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, progress)
	requireOpenAIWeeklyEstimate(t, progress, 176/0.06)
}

func TestOpenAIWeeklyEstimateMigratesV2WithoutBlanking(t *testing.T) {
	t.Parallel()

	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	account := &Account{
		ID:          8,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra: map[string]any{
			openAIWeeklyEstimateBaselineKey: map[string]any{
				"version":              2,
				"segment_percent":      38.0,
				"segment_cost":         142.0,
				"last_percent":         38.5,
				"last_cost":            160.0,
				"completed_estimate":   5000.0,
				"segment_max_estimate": 3800.0,
				"reset_at":             resetAt.Format(time.RFC3339Nano),
				"identity":             "workspace-a",
			},
		},
	}

	migrated := openAIWeeklyEstimateProgress(38.5, 160, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, migrated)
	requireOpenAIWeeklyEstimate(t, migrated, 5000)

	complete := openAIWeeklyEstimateProgress(39, 180, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, complete)
	requireOpenAIWeeklyEstimate(t, complete, 2498)
}

func TestOpenAIWeeklyEstimateV1BaselineSeedsDifferentialRate(t *testing.T) {
	t.Parallel()

	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	account := &Account{
		ID:          9,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra: map[string]any{
			openAIWeeklyEstimateBaselineKey: map[string]any{
				"percent":  37.0,
				"cost":     100.0,
				"reset_at": resetAt.Format(time.RFC3339Nano),
				"identity": "workspace-a",
			},
		},
	}

	complete := openAIWeeklyEstimateProgress(38, 142, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, complete)
	requireOpenAIWeeklyEstimate(t, complete, 2746)
}

func TestOpenAIWeeklyEstimateAtFullQuotaAlwaysEqualsAccountCost(t *testing.T) {
	t.Parallel()

	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	account := &Account{ID: 10, Extra: map[string]any{}}
	full := openAIWeeklyEstimateProgress(100, 108.05, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, full)
	requireOpenAIWeeklyEstimate(t, full, 108.05)

	zeroCostAccount := &Account{ID: 11, Extra: map[string]any{}}
	zeroCost := openAIWeeklyEstimateProgress(100, 0, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, zeroCostAccount, zeroCost)
	requireOpenAIWeeklyEstimate(t, zeroCost, 0)
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
	return &UsageProgress{
		Utilization: percent,
		ResetsAt:    &resetAt,
		WindowStats: &WindowStats{
			Cost: cost,
		},
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

func requireOpenAIWeeklyEstimateCheckpoint(t *testing.T, account *Account, wantPercent, wantCost float64) {
	t.Helper()
	raw, ok := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if !ok {
		t.Fatal("weekly estimate checkpoint state is missing")
	}
	percent, percentOK := parseOpenAIWeeklyEstimateNumber(raw, "checkpoint_percent")
	cost, costOK := parseOpenAIWeeklyEstimateNumber(raw, "checkpoint_cost")
	if !percentOK || math.Abs(percent-wantPercent) > 1e-9 {
		t.Fatalf("checkpoint percent = %v, want %v", percent, wantPercent)
	}
	if !costOK || math.Abs(cost-wantCost) > 1e-9 {
		t.Fatalf("checkpoint cost = %v, want %v", cost, wantCost)
	}
}
