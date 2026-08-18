package service

import (
	"math"
	"testing"
	"time"
)

func TestOpenAIWeeklyEstimateHoldsLastCompletedSegment(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          1,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra:       map[string]any{},
	}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)

	initial := openAIWeeklyEstimateProgress(37, 100, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, initial)
	requireOpenAIWeeklyEstimatePending(t, initial)

	partial := openAIWeeklyEstimateProgress(37.5, 125, resetAt.Add(30*time.Second))
	applyOpenAIWeeklyEstimateForTest(t, account, partial)
	requireOpenAIWeeklyEstimatePending(t, partial)

	completed := openAIWeeklyEstimateProgress(38, 142, resetAt.Add(time.Minute))
	applyOpenAIWeeklyEstimateForTest(t, account, completed)
	// The segment reached $5,000 while it was accumulating, so the lower
	// endpoint sample must not replace that maximum.
	requireOpenAIWeeklyEstimate(t, completed, 5000)

	samePercent := openAIWeeklyEstimateProgress(38, 150, resetAt.Add(2*time.Minute))
	applyOpenAIWeeklyEstimateForTest(t, account, samePercent)
	requireOpenAIWeeklyEstimate(t, samePercent, 5000)

	partialNext := openAIWeeklyEstimateProgress(38.5, 157, resetAt.Add(3*time.Minute))
	applyOpenAIWeeklyEstimateForTest(t, account, partialNext)
	requireOpenAIWeeklyEstimate(t, partialNext, 5000)

	partialNextMoreCost := openAIWeeklyEstimateProgress(38.5, 160, resetAt.Add(4*time.Minute))
	applyOpenAIWeeklyEstimateForTest(t, account, partialNextMoreCost)
	requireOpenAIWeeklyEstimate(t, partialNextMoreCost, 5000)

	almostComplete := openAIWeeklyEstimateProgress(38.99, 179, resetAt.Add(5*time.Minute))
	applyOpenAIWeeklyEstimateForTest(t, account, almostComplete)
	requireOpenAIWeeklyEstimate(t, almostComplete, 5000)

	nextCompleted := openAIWeeklyEstimateProgress(39, 180, resetAt.Add(6*time.Minute))
	applyOpenAIWeeklyEstimateForTest(t, account, nextCompleted)
	// A completed segment replaces the previous result even when it is lower;
	// this is a per-segment maximum, not an all-time maximum.
	requireOpenAIWeeklyEstimate(t, nextCompleted, 3800)
}

func TestOpenAIWeeklyEstimateWaitsForFullPercentAfterLogin(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          2,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra:       map[string]any{},
	}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(26.25, 100, resetAt))
	incomplete := openAIWeeklyEstimateProgress(27, 130, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, incomplete)
	requireOpenAIWeeklyEstimatePending(t, incomplete)

	complete := openAIWeeklyEstimateProgress(27.25, 140, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, complete)
	requireOpenAIWeeklyEstimate(t, complete, 4000)
}

func TestOpenAIWeeklyEstimateAveragesMultiPointJump(t *testing.T) {
	t.Parallel()

	account := &Account{ID: 3, Extra: map[string]any{}}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(40, 200, resetAt))

	progress := openAIWeeklyEstimateProgress(43, 320, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, progress)
	requireOpenAIWeeklyEstimate(t, progress, 4000)
}

func TestOpenAIWeeklyEstimateRestartsForWindowIdentityOrCostRollback(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          4,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra:       map[string]any{},
	}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(50, 1000, resetAt))
	withEstimate := openAIWeeklyEstimateProgress(51, 1060, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, withEstimate)
	requireOpenAIWeeklyEstimate(t, withEstimate, 6000)

	newWindowReset := resetAt.Add(7 * 24 * time.Hour)
	newWindow := openAIWeeklyEstimateProgress(12, 50, newWindowReset)
	applyOpenAIWeeklyEstimateForTest(t, account, newWindow)
	requireOpenAIWeeklyEstimatePending(t, newWindow)

	afterWindow := openAIWeeklyEstimateProgress(13, 90, newWindowReset)
	applyOpenAIWeeklyEstimateForTest(t, account, afterWindow)
	requireOpenAIWeeklyEstimate(t, afterWindow, 4000)

	account.Credentials["chatgpt_account_id"] = "workspace-b"
	newIdentity := openAIWeeklyEstimateProgress(14, 130, newWindowReset)
	applyOpenAIWeeklyEstimateForTest(t, account, newIdentity)
	requireOpenAIWeeklyEstimatePending(t, newIdentity)

	rollback := openAIWeeklyEstimateProgress(15, 120, newWindowReset)
	applyOpenAIWeeklyEstimateForTest(t, account, rollback)
	requireOpenAIWeeklyEstimatePending(t, rollback)
}

func TestOpenAIWeeklyEstimateMigratesLegacyBaselineWithoutBlanking(t *testing.T) {
	t.Parallel()

	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	account := &Account{
		ID:          5,
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

	progress := openAIWeeklyEstimateProgress(38, 142, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, progress)
	requireOpenAIWeeklyEstimate(t, progress, 4200)

	raw := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if got := parseExtraInt(raw["version"]); got != openAIWeeklyEstimateStateVersion {
		t.Fatalf("state version = %d, want %d", got, openAIWeeklyEstimateStateVersion)
	}
}

func applyOpenAIWeeklyEstimateForTest(t *testing.T, account *Account, progress *UsageProgress) {
	t.Helper()
	estimate, updates := calculateOpenAIWeeklyEstimate(account, progress)
	progress.WeeklyEstimateUSD = estimate
	for key, value := range updates {
		account.Extra[key] = value
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
