package service

import (
	"math"
	"testing"
	"time"
)

func TestOpenAIWeeklyEstimateUsesCumulativeUsageAfterLoginBaseline(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          1,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra:       map[string]any{},
	}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)

	first := openAIWeeklyEstimateProgress(37, 100, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, first)
	if first.WeeklyEstimateUSD != nil {
		t.Fatalf("initial estimate = %v, want collecting state", *first.WeeklyEstimateUSD)
	}

	second := openAIWeeklyEstimateProgress(38, 142, resetAt.Add(30*time.Second))
	applyOpenAIWeeklyEstimateForTest(t, account, second)
	requireOpenAIWeeklyEstimate(t, second, 4200)

	samePercent := openAIWeeklyEstimateProgress(38, 150, resetAt.Add(time.Minute))
	applyOpenAIWeeklyEstimateForTest(t, account, samePercent)
	requireOpenAIWeeklyEstimate(t, samePercent, 5000)

	third := openAIWeeklyEstimateProgress(39, 180, resetAt.Add(2*time.Minute))
	applyOpenAIWeeklyEstimateForTest(t, account, third)
	requireOpenAIWeeklyEstimate(t, third, 4000)
}

func TestOpenAIWeeklyEstimateExcludesUsageBeforeNewLoginBaseline(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          2,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra:       map[string]any{},
	}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(26.25, 100, resetAt))
	progress := openAIWeeklyEstimateProgress(30.25, 140, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, progress)
	requireOpenAIWeeklyEstimate(t, progress, 1000)
}

func TestOpenAIWeeklyEstimateDividesPercentJumps(t *testing.T) {
	t.Parallel()

	account := &Account{ID: 3, Extra: map[string]any{}}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(40, 200, resetAt))
	progress := openAIWeeklyEstimateProgress(43, 320, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, progress)
	requireOpenAIWeeklyEstimate(t, progress, 4000)
}

func TestOpenAIWeeklyEstimateRestartsForNewWindowOrIdentity(t *testing.T) {
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

	newWindow := openAIWeeklyEstimateProgress(12, 50, resetAt.Add(7*24*time.Hour))
	applyOpenAIWeeklyEstimateForTest(t, account, newWindow)
	if newWindow.WeeklyEstimateUSD != nil {
		t.Fatalf("new-window estimate = %v, want collecting state", *newWindow.WeeklyEstimateUSD)
	}

	account.Credentials["chatgpt_account_id"] = "workspace-b"
	newIdentity := openAIWeeklyEstimateProgress(13, 90, resetAt.Add(7*24*time.Hour))
	applyOpenAIWeeklyEstimateForTest(t, account, newIdentity)
	if newIdentity.WeeklyEstimateUSD != nil {
		t.Fatalf("new-identity estimate = %v, want collecting state", *newIdentity.WeeklyEstimateUSD)
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
