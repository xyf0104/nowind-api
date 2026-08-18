package service

import (
	"math"
	"testing"
	"time"
)

func TestOpenAIWeeklyEstimateHoldsUntilFullTwoPercentSegment(t *testing.T) {
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

	partial := openAIWeeklyEstimateProgress(38, 145, resetAt.Add(30*time.Second))
	applyOpenAIWeeklyEstimateForTest(t, account, partial)
	requireOpenAIWeeklyEstimatePending(t, partial)

	completed := openAIWeeklyEstimateProgress(39, 180, resetAt.Add(time.Minute))
	applyOpenAIWeeklyEstimateForTest(t, account, completed)
	// $80 / 2% = $40 per point; $180 already used + 61% * $40.
	requireOpenAIWeeklyEstimate(t, completed, 2620)

	samePercent := openAIWeeklyEstimateProgress(39, 190, resetAt.Add(2*time.Minute))
	applyOpenAIWeeklyEstimateForTest(t, account, samePercent)
	requireOpenAIWeeklyEstimate(t, samePercent, 2620)

	partialNext := openAIWeeklyEstimateProgress(40, 220, resetAt.Add(3*time.Minute))
	applyOpenAIWeeklyEstimateForTest(t, account, partialNext)
	requireOpenAIWeeklyEstimate(t, partialNext, 2620)
}

func TestOpenAIWeeklyEstimateSmoothsRecentTwoPercentSamples(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          2,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra:       map[string]any{},
	}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)

	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(10, 100, resetAt))
	first := openAIWeeklyEstimateProgress(12, 190, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, first)
	requireOpenAIWeeklyEstimate(t, first, 4150) // $45/%

	second := openAIWeeklyEstimateProgress(14, 250, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, second)
	// $45/% and $30/% remain valid samples. The newer one has a 1.25 weight.
	smoothedRate := (45.0 + 30.0*1.25) / 2.25
	requireOpenAIWeeklyEstimate(t, second, 250+86*smoothedRate)
}

func TestOpenAIWeeklyEstimateFiltersSingleObviousOutlier(t *testing.T) {
	t.Parallel()

	rate, ok := openAIWeeklyEstimateSmoothedRate([]float64{30, 31, 200})
	if !ok {
		t.Fatal("smoothed rate is unavailable")
	}
	want := (30.0 + 31.0*1.25) / 2.25
	if math.Abs(rate-want) > 1e-9 {
		t.Fatalf("smoothed rate = %v, want %v", rate, want)
	}
}

func TestOpenAIWeeklyEstimateAveragesMultiPointJump(t *testing.T) {
	t.Parallel()

	account := &Account{ID: 3, Extra: map[string]any{}}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(40, 200, resetAt))

	progress := openAIWeeklyEstimateProgress(43, 320, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, progress)
	// Missing intermediate observations use the whole 3% interval: $120 / 3%.
	requireOpenAIWeeklyEstimate(t, progress, 2600)
}

func TestOpenAIWeeklyEstimateRebasesUnbilledExternalUsage(t *testing.T) {
	t.Parallel()

	account := &Account{ID: 4, Extra: map[string]any{}}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 100, resetAt))

	externalOnly := openAIWeeklyEstimateProgress(22, 100, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, externalOnly)
	requireOpenAIWeeklyEstimatePending(t, externalOnly)

	localSegment := openAIWeeklyEstimateProgress(24, 180, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, localSegment)
	requireOpenAIWeeklyEstimate(t, localSegment, 3220)
}

func TestOpenAIWeeklyEstimateRestartsForWindowIdentityOrCostRollback(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          5,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-a"},
		Extra:       map[string]any{},
	}
	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	applyOpenAIWeeklyEstimateForTest(t, account, openAIWeeklyEstimateProgress(50, 1000, resetAt))
	withEstimate := openAIWeeklyEstimateProgress(52, 1080, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, withEstimate)
	requireOpenAIWeeklyEstimate(t, withEstimate, 3000)

	newWindowReset := resetAt.Add(7 * 24 * time.Hour)
	newWindow := openAIWeeklyEstimateProgress(12, 50, newWindowReset)
	applyOpenAIWeeklyEstimateForTest(t, account, newWindow)
	requireOpenAIWeeklyEstimatePending(t, newWindow)

	afterWindow := openAIWeeklyEstimateProgress(14, 130, newWindowReset)
	applyOpenAIWeeklyEstimateForTest(t, account, afterWindow)
	requireOpenAIWeeklyEstimate(t, afterWindow, 3570)

	account.Credentials["chatgpt_account_id"] = "workspace-b"
	newIdentity := openAIWeeklyEstimateProgress(15, 150, newWindowReset)
	applyOpenAIWeeklyEstimateForTest(t, account, newIdentity)
	requireOpenAIWeeklyEstimatePending(t, newIdentity)

	rollback := openAIWeeklyEstimateProgress(17, 140, newWindowReset)
	applyOpenAIWeeklyEstimateForTest(t, account, rollback)
	requireOpenAIWeeklyEstimatePending(t, rollback)
}

func TestOpenAIWeeklyEstimateMigratesV2WithoutBlanking(t *testing.T) {
	t.Parallel()

	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	account := &Account{
		ID:          6,
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

	raw := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if got := parseExtraInt(raw["version"]); got != openAIWeeklyEstimateStateVersion {
		t.Fatalf("state version = %d, want %d", got, openAIWeeklyEstimateStateVersion)
	}

	incomplete := openAIWeeklyEstimateProgress(40, 205, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, incomplete)
	requireOpenAIWeeklyEstimate(t, incomplete, 5000)

	completed := openAIWeeklyEstimateProgress(40.5, 220, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, completed)
	requireOpenAIWeeklyEstimate(t, completed, 2005)
}

func TestOpenAIWeeklyEstimateV1BaselineRequiresFullTwoPercent(t *testing.T) {
	t.Parallel()

	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	account := &Account{
		ID:          7,
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

	incomplete := openAIWeeklyEstimateProgress(38, 142, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, incomplete)
	requireOpenAIWeeklyEstimatePending(t, incomplete)

	complete := openAIWeeklyEstimateProgress(39, 180, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, complete)
	requireOpenAIWeeklyEstimate(t, complete, 2620)
}

func TestOpenAIWeeklyEstimateAtFullQuotaAlwaysEqualsAccountCost(t *testing.T) {
	t.Parallel()

	resetAt := time.Date(2026, time.August, 25, 5, 17, 50, 0, time.UTC)
	account := &Account{ID: 8, Extra: map[string]any{}}
	full := openAIWeeklyEstimateProgress(100, 108.05, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, account, full)
	requireOpenAIWeeklyEstimate(t, full, 108.05)

	zeroCostAccount := &Account{ID: 9, Extra: map[string]any{}}
	zeroCost := openAIWeeklyEstimateProgress(100, 0, resetAt)
	applyOpenAIWeeklyEstimateForTest(t, zeroCostAccount, zeroCost)
	requireOpenAIWeeklyEstimate(t, zeroCost, 0)
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
