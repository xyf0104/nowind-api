package service

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
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

	// Raw progress inside the next unfinished 1% interval stays frozen.
	stillTwentyOne := openAIWeeklyEstimateProgress(21.9, 30, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, stillTwentyOne, 30, 30, true, firstAt.Add(2*time.Minute))
	requireOpenAIWeeklyEstimate(t, stillTwentyOne, 2000)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 20, 0, 21, 30)

	staleTwentyOne := openAIWeeklyEstimateProgress(21.9, 25, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, staleTwentyOne, 25, 25, true, firstAt.Add(time.Minute))
	requireOpenAIWeeklyEstimate(t, staleTwentyOne, 2000)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 20, 0, 21, 30)

	// Keep averaging from the join baseline, not from the latest one-percent
	// interval: $120 / (25 - 20) * 100 = $2400.
	atTwentyFive := openAIWeeklyEstimateProgress(25, 120, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, atTwentyFive, 120, 120, true, firstAt.Add(4*time.Minute))
	requireOpenAIWeeklyEstimate(t, atTwentyFive, 2400)
	requireOpenAIWeeklyFrozenEstimateState(t, account, 20, 0, 25, 120)
}

func TestOpenAIWeeklyFrozenEstimateUsesLegacyRuleBFromZero(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(11)
	resetAt := openAIWeeklyEstimateTestResetAt()
	firstAt := time.Date(2026, time.August, 27, 8, 0, 0, 0, time.UTC)

	zero := openAIWeeklyEstimateProgress(0, 0, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, zero, 0, 0, true, firstAt)
	requireOpenAIWeeklyEstimatePending(t, zero)

	one := openAIWeeklyEstimateProgress(1, 10, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, one, 10, 10, true, firstAt.Add(time.Minute))
	requireOpenAIWeeklyEstimatePending(t, one)

	two := openAIWeeklyEstimateProgress(2, 20, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, two, 20, 20, true, firstAt.Add(2*time.Minute))
	requireOpenAIWeeklyEstimate(t, two, 2000)

	// Rule B freezes the displayed estimate throughout this integer bucket.
	twoPointNine := openAIWeeklyEstimateProgress(2.9, 30, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, twoPointNine, 30, 30, true, firstAt.Add(3*time.Minute))
	requireOpenAIWeeklyEstimate(t, twoPointNine, 2000)
}

func TestOpenAIWeeklyFrozenEstimateUsesAlignedMidWindowBaseline(t *testing.T) {
	t.Parallel()
	resetAt := openAIWeeklyEstimateTestResetAt()
	for _, tc := range []struct {
		name, identity            string
		baseline, current         float64
		baselineCost, currentCost float64
		want                      float64
	}{
		{name: "account 352 style", identity: "b211", baseline: 17, current: 65, baselineCost: 0, currentCost: 854.289299, want: 1779.7693729166667},
		{name: "account 353 style", identity: "330", baseline: 11, current: 49, baselineCost: 0, currentCost: 602.135073, want: 1584.5659815789474},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(12)
			account.Credentials["chatgpt_account_id"] = tc.identity
			firstAt := time.Date(2026, time.August, 27, 9, 0, 0, 0, time.UTC)
			baseline := openAIWeeklyEstimateProgress(tc.baseline, tc.baselineCost, resetAt)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, baseline, tc.baselineCost, tc.baselineCost, true, firstAt)
			requireOpenAIWeeklyEstimatePending(t, baseline)
			current := openAIWeeklyEstimateProgress(tc.current, tc.currentCost, resetAt)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, current, tc.currentCost, tc.currentCost, true, firstAt.Add(time.Hour))
			requireOpenAIWeeklyEstimate(t, current, tc.want)
			state, ok := readOpenAIWeeklyFrozenEstimateState(account.Extra)
			if !ok || state.Mode != openAIWeeklyEstimateModeJoinAverage || state.BaselineSource != "first_aligned_observation" {
				t.Fatalf("state mode/source = (%q, %q), want aligned observation without a login claim", state.Mode, state.BaselineSource)
			}
		})
	}
}

func TestOpenAIWeeklyFrozenEstimateFirstMidWindowObservationStaysPending(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(2)
	resetAt := openAIWeeklyEstimateTestResetAt()
	observedAt := time.Date(2026, time.August, 26, 8, 15, 0, 0, time.UTC)

	// A first billed observation without a persisted zero-percent marker is a
	// mid-window candidate and must wait for a complete interval.
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
	requireOpenAIWeeklyEstimate(t, progress, 40/1*100)
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
	requireOpenAIWeeklyEstimate(t, atEleven, 20/1*100)

	account.Credentials["access_token"] = "access-rotated"
	account.Credentials["refresh_token"] = "refresh-rotated"
	stillEleven := openAIWeeklyEstimateProgress(11, 290, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, stillEleven, 290, 290, true, observedAt.Add(2*time.Minute))
	requireOpenAIWeeklyEstimate(t, stillEleven, 2000)
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
			repeat := openAIWeeklyEstimateProgress(tc.percent, tc.cost, tc.resetAt)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, repeat, tc.cost, tc.cost, true, observedAt.Add(3*time.Minute))
			requireOpenAIWeeklyEstimatePending(t, repeat)
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
	if got := parseExtraInt(raw["version"]); got != openAIWeeklyFrozenEstimateTwoRuleStateVersion {
		t.Fatalf("migrated state version = %d, want %d", got, openAIWeeklyFrozenEstimateTwoRuleStateVersion)
	}
	if _, exists := raw["estimate_usd"]; exists {
		t.Fatal("unclassified legacy state must not publish an estimate")
	}
	if evidence, ok := raw["legacy_evidence"].(map[string]any); !ok || evidence["estimate_usd"] != 709.31 {
		t.Fatal("original legacy evidence was not retained")
	}

	next := openAIWeeklyEstimateProgress(20, 147.93, resetAt)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, next, 147.93, 147.93, true, observedAt.Add(time.Minute))
	requireOpenAIWeeklyEstimatePending(t, next)
}

func TestOpenAIWeeklyFrozenEstimateConfirmedTwoRuleTables(t *testing.T) {
	t.Parallel()
	type step struct {
		percent, cost, want float64
		pending             bool
	}
	cases := []struct {
		name  string
		steps []step
	}{
		{"Rule B 260 divided by nine percent", []step{
			{percent: 0, pending: true}, {percent: 1, cost: 10, pending: true},
			{percent: 10, cost: 260, want: 260 / .09},
			{percent: 10.9, cost: 9999, want: 260 / .09},
		}},
		{"mid join cumulative not latest interval", []step{
			{percent: 20, pending: true}, {percent: 21, cost: 20, want: 2000},
			{percent: 21, cost: 25, want: 2000},
			{percent: 21.9, cost: 30, want: 2000},
			{percent: 25, cost: 120, want: 2400},
		}},
		{"eleven to forty five", []step{
			{percent: 11, pending: true}, {percent: 45, cost: 853.5, want: 853.5 / 34 * 100},
		}},
		{"raw fractional endpoints", []step{
			{percent: 20.4, pending: true},
			{percent: 21.399, cost: 19, pending: true},
			{percent: 21.4, cost: 20, want: 2000},
			{percent: 22, cost: 30, want: 2000},
			{percent: 22.4, cost: 60, want: 3000},
			{percent: 24.9, cost: 135, want: 3000},
		}},
		{"fractional subtraction below one ulp", []step{
			{percent: .4, pending: true}, {percent: 1.4, cost: 20, want: 2000},
		}},
		{"nonzero baseline cost is subtracted", []step{
			{percent: 8, cost: 200, pending: true},
			{percent: 9, cost: 240, want: 4000},
			{percent: 12.5, cost: 290, want: 2000},
		}},
		{"no clamping frozen estimate to live aligned cost", []step{
			{percent: 20, cost: 1000, pending: true},
			{percent: 21, cost: 1001, want: 100},
			{percent: 21.9, cost: 5000, want: 100},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(100)
			firstAt := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
			for i, sample := range tc.steps {
				progress := openAIWeeklyEstimateProgress(sample.percent, sample.cost, openAIWeeklyEstimateTestResetAt())
				applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, sample.cost, sample.cost, true, firstAt.Add(time.Duration(i)*time.Minute))
				if sample.pending {
					requireOpenAIWeeklyEstimatePending(t, progress)
				} else {
					requireOpenAIWeeklyEstimate(t, progress, sample.want)
				}
				state := requireOpenAIWeeklyTwoRuleState(t, account)
				if state.SnapshotPercent != sample.percent {
					t.Fatalf("raw percent = %.17g, want %.17g", state.SnapshotPercent, sample.percent)
				}
			}
		})
	}
}

func TestOpenAIWeeklyFrozenEstimateRollbacksRequireNewFullInterval(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		percent, cost float64
	}{
		{"percent rollback", 9, 30},
		{"same percent cost rollback", 11, 10},
		{"advanced percent cost rollback", 12, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(101)
			at := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
			reset := openAIWeeklyEstimateTestResetAt()
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, openAIWeeklyEstimateProgress(10, 0, reset), 0, 0, true, at)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, openAIWeeklyEstimateProgress(11, 20, reset), 20, 20, true, at.Add(time.Minute))
			for i, p := range []float64{tc.percent, tc.percent, tc.percent + .9} {
				progress := openAIWeeklyEstimateProgress(p, tc.cost+float64(i), reset)
				applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, tc.cost+float64(i), tc.cost+float64(i), true, at.Add(time.Duration(i+2)*time.Minute))
				requireOpenAIWeeklyEstimatePending(t, progress)
			}
			state := requireOpenAIWeeklyTwoRuleState(t, account)
			if state.Mode != openAIWeeklyEstimateModeJoinAverage {
				t.Fatalf("nonzero rollback became %q instead of a pending cumulative rebase", state.Mode)
			}
			progress := openAIWeeklyEstimateProgress(tc.percent+1, tc.cost+10, reset)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, tc.cost+10, tc.cost+10, true, at.Add(5*time.Minute))
			requireOpenAIWeeklyEstimate(t, progress, 1000)
		})
	}
}

func TestOpenAIWeeklyFrozenEstimateObservedZeroResetUsesRuleB(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(102)
	at := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	reset := openAIWeeklyEstimateTestResetAt()
	for i, sample := range []struct{ percent, cost float64 }{{20, 0}, {21, 20}, {0, 0}, {0, 0}, {1, 10}, {10, 260}} {
		progress := openAIWeeklyEstimateProgress(sample.percent, sample.cost, reset)
		applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, sample.cost, sample.cost, true, at.Add(time.Duration(i)*time.Minute))
		if i >= 2 && i < 5 {
			requireOpenAIWeeklyEstimatePending(t, progress)
		}
		if i == 5 {
			requireOpenAIWeeklyEstimate(t, progress, 260/.09)
		}
	}
	if state := requireOpenAIWeeklyTwoRuleState(t, account); state.Mode != openAIWeeklyEstimateModeLegacy {
		t.Fatalf("observed zero reset mode = %q", state.Mode)
	}
}

func TestOpenAIWeeklyFrozenEstimateRuleBResetWaitsForFullInterval(t *testing.T) {
	t.Parallel()
	for _, newWindow := range []bool{false, true} {
		account := newOpenAIWeeklyEstimateTestAccount(111)
		at := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
		reset := openAIWeeklyEstimateTestResetAt()
		applyOpenAIWeeklyFrozenEstimateForTest(t, account, openAIWeeklyEstimateProgress(0, 0, reset), 0, 0, true, at)
		applyOpenAIWeeklyFrozenEstimateForTest(t, account, openAIWeeklyEstimateProgress(11, 300, reset), 300, 300, true, at.Add(time.Minute))
		if newWindow {
			reset = reset.Add(7 * 24 * time.Hour)
		}
		for i, p := range []float64{9.9, 9.9, 10} {
			progress := openAIWeeklyEstimateProgress(p, 250+float64(i), reset)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, 250+float64(i), 250+float64(i), true, at.Add(time.Duration(i+2)*time.Minute))
			requireOpenAIWeeklyEstimatePending(t, progress)
		}
		progress := openAIWeeklyEstimateProgress(10.9, 260, reset)
		applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, 260, 260, true, at.Add(5*time.Minute))
		requireOpenAIWeeklyEstimate(t, progress, 260/.09)
		if state := requireOpenAIWeeklyTwoRuleState(t, account); state.Mode != openAIWeeklyEstimateModeLegacy {
			t.Fatal("known Rule B/reset classification was lost")
		}
	}
}

func TestOpenAIWeeklyFrozenEstimateExternalOnlyRebasesBothModes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                      string
		start, endpoint, external float64
		cost                      float64
	}{
		{"Rule B before first estimate", 0, 0, 1, 0},
		{"Rule B after estimate", 0, 2, 3, 20},
		{"join before first estimate", 20, 20, 21, 0},
		{"join after estimate", 20, 21, 22, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(103)
			at := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
			reset := openAIWeeklyEstimateTestResetAt()
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, openAIWeeklyEstimateProgress(tc.start, 0, reset), 0, 0, true, at)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, openAIWeeklyEstimateProgress(tc.endpoint, tc.cost, reset), tc.cost, tc.cost, true, at.Add(time.Minute))
			for i := 0; i < 2; i++ {
				progress := openAIWeeklyEstimateProgress(tc.external, tc.cost, reset)
				applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, tc.cost, tc.cost, true, at.Add(time.Duration(i+2)*time.Minute))
				requireOpenAIWeeklyEstimatePending(t, progress)
			}
			state := requireOpenAIWeeklyTwoRuleState(t, account)
			if state.Mode != openAIWeeklyEstimateModeJoinAverage || state.BaselineSource != "external_only_rebase" ||
				state.BaselinePercent != tc.external || state.BaselineCost != tc.cost {
				t.Fatalf("external-only rebase state = %+v", state)
			}
			progress := openAIWeeklyEstimateProgress(tc.external+1, tc.cost+30, reset)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, tc.cost+30, tc.cost+30, true, at.Add(4*time.Minute))
			requireOpenAIWeeklyEstimate(t, progress, 3000)
		})
	}
}

func TestOpenAIWeeklyFrozenEstimateExternalDetectionUsesAlignedBoundary(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(104)
	at := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	reset := openAIWeeklyEstimateTestResetAt()
	for i, sample := range []struct{ percent, cost float64 }{{20, 0}, {20.5, 10}, {21, 10}} {
		progress := openAIWeeklyEstimateProgress(sample.percent, sample.cost, reset)
		applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, sample.cost, sample.cost, true, at.Add(time.Duration(i)*time.Minute))
		if i == 2 {
			requireOpenAIWeeklyEstimate(t, progress, 1000)
		}
	}
	repeat := openAIWeeklyEstimateProgress(21, 25, reset)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, repeat, 25, 25, true, at.Add(3*time.Minute))
	requireOpenAIWeeklyEstimate(t, repeat, 1000)
	external := openAIWeeklyEstimateProgress(22, 25, reset)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, external, 25, 25, true, at.Add(4*time.Minute))
	requireOpenAIWeeklyEstimatePending(t, external)
}

func TestOpenAIWeeklyFrozenEstimateRejectsOutOfOrderIncludingFullQuota(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(105)
	at := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	reset := openAIWeeklyEstimateTestResetAt()
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, openAIWeeklyEstimateProgress(0, 0, reset), 0, 0, true, at)
	applyOpenAIWeeklyFrozenEstimateForTest(t, account, openAIWeeklyEstimateProgress(10, 260, reset), 260, 260, true, at.Add(2*time.Minute))
	before, _ := json.Marshal(account.Extra)
	for _, sample := range []struct {
		percent, cost float64
		at            time.Time
	}{{100, 900, at.Add(time.Minute)}, {100, 900, at.Add(2 * time.Minute)}, {9, 240, at.Add(time.Minute)}, {10, 240, at.Add(2 * time.Minute)}} {
		progress := openAIWeeklyEstimateProgress(sample.percent, sample.cost, reset)
		estimate, updates := calculateOpenAIWeeklyFrozenEstimate(account, progress, sample.cost, sample.cost, true, sample.at)
		progress.WeeklyEstimateUSD = estimate
		requireOpenAIWeeklyEstimate(t, progress, 260/.09)
		if updates != nil {
			t.Fatal("stale or same-time conflicting sample produced a write")
		}
	}
	after, _ := json.Marshal(account.Extra)
	if string(before) != string(after) {
		t.Fatal("calculator mutated accepted state")
	}
}

func TestOpenAIWeeklyFrozenEstimateFullQuotaPersistsTerminalIncludingZero(t *testing.T) {
	t.Parallel()
	for _, cost := range []float64{0, 108.05} {
		t.Run(fmtWeeklyTestCost(cost), func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(106)
			at := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
			reset := openAIWeeklyEstimateTestResetAt()
			progress := openAIWeeklyEstimateProgress(100, cost, reset)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, 999, cost, false, at)
			requireOpenAIWeeklyEstimate(t, progress, cost)
			state := requireOpenAIWeeklyTwoRuleState(t, account)
			if state.SnapshotPercent != 100 || state.CompletedPercent != 100 || !state.HasEstimate || state.ObservedAt != at {
				t.Fatalf("terminal observation not retained: %+v", state)
			}
			raw := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
			if raw["terminal"] != true {
				t.Fatal("missing serialized terminal marker")
			}
			encoded, err := json.Marshal(account.Extra)
			if err != nil {
				t.Fatal(err)
			}
			var restored map[string]any
			if err := json.Unmarshal(encoded, &restored); err != nil {
				t.Fatal(err)
			}
			account.Extra = restored
			state = requireOpenAIWeeklyTwoRuleState(t, account)
			if estimate := state.value(); estimate == nil || *estimate != cost {
				t.Fatal("terminal value did not survive JSON round trip")
			}
			for i := 0; i < 2; i++ {
				regression := openAIWeeklyEstimateProgress(99, cost, reset)
				applyOpenAIWeeklyFrozenEstimateForTest(t, account, regression, cost, cost, true, at.Add(time.Duration(i+1)*time.Minute))
				requireOpenAIWeeklyEstimatePending(t, regression)
			}
			staleFull := openAIWeeklyEstimateProgress(100, cost+1, reset)
			estimate, updates := calculateOpenAIWeeklyFrozenEstimate(account, staleFull, cost+1, cost+1, true, at)
			if estimate != nil || updates != nil {
				t.Fatal("old 100% overwrote a later pending regression")
			}
		})
	}
}

func fmtWeeklyTestCost(cost float64) string {
	if cost == 0 {
		return "zero"
	}
	return "nonzero"
}

func TestOpenAIWeeklyFrozenEstimateFullQuotaValidatesScopeAndTime(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*Account, *UsageProgress, *time.Time)
	}{
		{"missing identity", func(a *Account, _ *UsageProgress, _ *time.Time) { delete(a.Credentials, "chatgpt_account_id") }},
		{"changed identity needs lifecycle invalidation", func(a *Account, _ *UsageProgress, _ *time.Time) { a.Credentials["chatgpt_account_id"] = "different" }},
		{"missing observation time", func(_ *Account, _ *UsageProgress, at *time.Time) { *at = time.Time{} }},
		{"future observation", func(_ *Account, _ *UsageProgress, at *time.Time) { *at = time.Now().Add(time.Hour) }},
		{"expired window", func(_ *Account, p *UsageProgress, _ *time.Time) {
			expired := time.Now().Add(-time.Hour)
			p.ResetsAt = &expired
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(107)
			at := time.Now().UTC().Add(-time.Minute)
			reset := openAIWeeklyEstimateTestResetAt()
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 0, reset), 0, 0, true, at)
			progress := openAIWeeklyEstimateProgress(100, 0, reset)
			at = at.Add(time.Second)
			tc.mutate(account, progress, &at)
			estimate, updates := calculateOpenAIWeeklyFrozenEstimate(account, progress, 0, 0, true, at)
			if estimate != nil || updates != nil {
				t.Fatal("unvalidated 100% sample returned an exact result or changed state")
			}
		})
	}
}

func TestOpenAIWeeklyFrozenEstimateV14UnknownClassificationStaysPending(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"", openAIWeeklyEstimateModeLegacy, openAIWeeklyEstimateModeJoinAverage} {
		t.Run("mode="+mode, func(t *testing.T) {
			account := newOpenAIWeeklyEstimateTestAccount(108)
			at := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
			reset := openAIWeeklyEstimateTestResetAt()
			raw := map[string]any{
				"version": 14, "baseline_percent": 20.0, "baseline_cost": 0.0, "percent_bucket": 25.0,
				"snapshot_percent": 25.0,
				"snapshot_cost":    120.0, "reset_at": reset.Format(time.RFC3339Nano), "identity": "workspace-a",
				"observed_at": at.Format(time.RFC3339Nano), "estimate_usd": 500.0, "has_weekly_estimate": true,
			}
			if mode != "" {
				raw["mode"] = mode
				raw["baseline_source"] = "legacy_rule_b_compatibility_v14"
				if mode == openAIWeeklyEstimateModeJoinAverage {
					raw["baseline_source"] = "v14_zero_cost_baseline"
				}
			}
			account.Extra[openAIWeeklyEstimateBaselineKey] = raw
			for i, percent := range []float64{25, 26, 30} {
				cost := 120 + float64(i)*20
				progress := openAIWeeklyEstimateProgress(percent, cost, reset)
				applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, cost, cost, true, at.Add(time.Duration(i)*time.Minute))
				requireOpenAIWeeklyEstimatePending(t, progress)
				state := requireOpenAIWeeklyTwoRuleState(t, account)
				if state.Mode != openAIWeeklyEstimateModeUnknown {
					t.Fatal("legacy classification was invented")
				}
			}
			evidence := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)["legacy_evidence"]
			if !reflect.DeepEqual(evidence, raw) {
				t.Fatal("raw legacy history was not retained")
			}
		})
	}
}

func TestOpenAIWeeklyFrozenEstimateV14KnownJoinRequiresNewRawInterval(t *testing.T) {
	t.Parallel()
	account := newOpenAIWeeklyEstimateTestAccount(109)
	at := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	reset := openAIWeeklyEstimateTestResetAt()
	legacy := newOpenAIWeeklyFrozenEstimateState(20, 0, reset, "workspace-a", at)
	legacy.SnapshotPercent, legacy.SnapshotCost, legacy.PercentBucket = 25.4, 120, 25
	legacy.CompletedPercent, legacy.CompletedCost = 25, 120
	legacy.HasEstimate, legacy.EstimateUSD = true, 2400
	raw := openAIWeeklyFrozenEstimateStateUpdate(legacy)[openAIWeeklyEstimateBaselineKey].(map[string]any)
	raw["version"] = 14
	account.Extra[openAIWeeklyEstimateBaselineKey] = raw
	for i, sample := range []struct{ percent, cost float64 }{{25.4, 120}, {26, 140}, {26.4, 160}} {
		progress := openAIWeeklyEstimateProgress(sample.percent, sample.cost, reset)
		applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, sample.cost, sample.cost, true, at.Add(time.Duration(i)*time.Minute))
		if i < 2 {
			requireOpenAIWeeklyEstimatePending(t, progress)
		} else {
			requireOpenAIWeeklyEstimate(t, progress, 2500)
		}
	}
	state := requireOpenAIWeeklyTwoRuleState(t, account)
	if state.BaselinePercent != 20 || state.CompletedPercent != 26.4 {
		t.Fatal("migration changed the cumulative baseline or floored the new endpoint")
	}
}

func TestOpenAIWeeklyFrozenEstimateMalformedAndFutureStateConservative(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	for _, version := range []int{14, 15, 16} {
		account := newOpenAIWeeklyEstimateTestAccount(110)
		raw := map[string]any{"version": version, "baseline_percent": map[string]any{"bad": true}}
		account.Extra[openAIWeeklyEstimateBaselineKey] = raw
		progress := openAIWeeklyEstimateProgress(20, 100, openAIWeeklyEstimateTestResetAt())
		estimate, updates := calculateOpenAIWeeklyFrozenEstimate(account, progress, 100, 100, true, at)
		if estimate != nil {
			t.Fatal("unreadable state produced an estimate")
		}
		if version == 16 {
			if updates != nil {
				t.Fatal("future state version was downgraded")
			}
			continue
		}
		mergeAccountExtra(account, updates)
		if state := requireOpenAIWeeklyTwoRuleState(t, account); state.Mode != openAIWeeklyEstimateModeUnknown {
			t.Fatal("malformed old state was called a fresh login")
		}
	}
}

type weeklyEstimateEntryCASRepo struct {
	AccountRepository
	accept bool
	calls  int
}

func (r *weeklyEstimateEntryCASRepo) CompareAndSwapOpenAIWeeklyState(context.Context, *Account, map[string]any) (bool, error) {
	r.calls++
	return r.accept, nil
}

func TestOpenAIWeeklyFrozenEstimateEntryPathsValidateFullQuota(t *testing.T) {
	t.Parallel()
	for _, readOnly := range []bool{false, true} {
		name := "active"
		if readOnly {
			name = "read only"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name  string
				stale bool
				cost  float64
			}{{name: "zero exact"}, {name: "nonzero exact", cost: 108.05}, {name: "older full quota", stale: true, cost: 999}} {
				t.Run(tc.name, func(t *testing.T) {
					now := time.Now().UTC().Truncate(time.Microsecond)
					account := newOpenAIWeeklyEstimateTestAccount(112)
					reset := openAIWeeklyEstimateTestResetAt()
					if tc.stale {
						applyOpenAIWeeklyFrozenEstimateForTest(t, account, openAIWeeklyEstimateProgress(20, 0, reset), 0, 0, true, now.Add(-3*time.Minute))
						applyOpenAIWeeklyFrozenEstimateForTest(t, account, openAIWeeklyEstimateProgress(21, 20, reset), 20, 20, true, now.Add(-time.Minute))
					}
					account.Extra["codex_usage_updated_at"] = now.Add(-2 * time.Minute).Format(time.RFC3339Nano)
					before, _ := json.Marshal(account.Extra)
					repo := &weeklyEstimateEntryCASRepo{accept: true}
					svc := &AccountUsageService{accountRepo: repo}
					progress := openAIWeeklyEstimateProgress(100, tc.cost, reset)
					stats := &usagestats.AccountStats{Cost: tc.cost}
					if readOnly {
						svc.applyOpenAIWeeklyEstimateReadOnly(context.Background(), account, progress, stats, now)
					} else {
						svc.applyOpenAIWeeklyEstimate(context.Background(), account, progress, stats, now)
					}
					want := tc.cost
					if tc.stale {
						want = 2000
					}
					requireOpenAIWeeklyEstimate(t, progress, want)
					if readOnly || tc.stale {
						after, _ := json.Marshal(account.Extra)
						if repo.calls != 0 || string(before) != string(after) {
							t.Fatal("read-only/stale full quota advanced durable state")
						}
					} else {
						state := requireOpenAIWeeklyTwoRuleState(t, account)
						if repo.calls != 1 || state.SnapshotPercent != 100 {
							t.Fatal("active full quota bypassed terminal CAS")
						}
					}
				})
			}
		})
	}
}

func TestOpenAIWeeklyFrozenEstimateEntryFullQuotaCASLossIsNotPublished(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	account := newOpenAIWeeklyEstimateTestAccount(113)
	account.Extra["codex_usage_updated_at"] = now.Add(-time.Minute).Format(time.RFC3339Nano)
	repo := &weeklyEstimateEntryCASRepo{}
	svc := &AccountUsageService{accountRepo: repo}
	progress := openAIWeeklyEstimateProgress(100, 0, openAIWeeklyEstimateTestResetAt())
	oldEstimate := 123.0
	progress.WeeklyEstimateUSD = &oldEstimate
	svc.applyOpenAIWeeklyEstimate(context.Background(), account, progress, &usagestats.AccountStats{}, now)
	requireOpenAIWeeklyEstimatePending(t, progress)
	if repo.calls != 1 || account.Extra[openAIWeeklyEstimateBaselineKey] != nil {
		t.Fatal("lost terminal CAS mutated the account")
	}
}

func TestOpenAIWeeklyFrozenEstimateEntryFullQuotaNeedsObservation(t *testing.T) {
	t.Parallel()
	for _, readOnly := range []bool{false, true} {
		account := newOpenAIWeeklyEstimateTestAccount(114)
		repo := &weeklyEstimateEntryCASRepo{accept: true}
		svc := &AccountUsageService{accountRepo: repo}
		progress := openAIWeeklyEstimateProgress(100, 0, openAIWeeklyEstimateTestResetAt())
		if readOnly {
			svc.applyOpenAIWeeklyEstimateReadOnly(context.Background(), account, progress, &usagestats.AccountStats{}, time.Now())
		} else {
			svc.applyOpenAIWeeklyEstimate(context.Background(), account, progress, &usagestats.AccountStats{}, time.Now())
		}
		requireOpenAIWeeklyEstimatePending(t, progress)
		if repo.calls != 0 {
			t.Fatal("unobserved 100% was persisted")
		}
	}
}

func TestOpenAIWeeklyFrozenEstimateExplicitBaselineInitializer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                 string
		percent, cost, nextPercent, nextCost float64
		mode                                 string
		want                                 float64
	}{
		{"zero start", 0, 0, 10, 260, openAIWeeklyEstimateModeLegacy, 260 / .09},
		{"raw mid join", 20.4, 0, 21.4, 20, openAIWeeklyEstimateModeJoinAverage, 2000},
		{"explicit nonzero baseline cost", 11, 10, 45, 863.5, openAIWeeklyEstimateModeJoinAverage, 853.5 / 34 * 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := time.Now().UTC().Add(-time.Hour)
			reset := at.Add(7 * 24 * time.Hour)
			state, ok := newOpenAIWeeklyFrozenEstimateStateFromBaseline(tc.percent, tc.cost, reset, "workspace-a", at, "verified_history_inception")
			if !ok || state.Mode != tc.mode || state.HasEstimate || !state.AwaitingInterval || state.value() != nil ||
				state.BaselineSource != "verified_history_inception" || state.Identity != "workspace-a" ||
				!state.ObservedAt.Equal(at) || !state.ResetAt.Equal(reset) {
				t.Fatalf("invalid explicit pending baseline: %+v, ok=%v", state, ok)
			}
			account := newOpenAIWeeklyEstimateTestAccount(115)
			account.Extra = openAIWeeklyFrozenEstimateStateUpdate(state)
			progress := openAIWeeklyEstimateProgress(tc.nextPercent, tc.nextCost, reset)
			applyOpenAIWeeklyFrozenEstimateForTest(t, account, progress, tc.nextCost, tc.nextCost, true, at.Add(time.Minute))
			requireOpenAIWeeklyEstimate(t, progress, tc.want)
			current := requireOpenAIWeeklyTwoRuleState(t, account)
			if current.BaselineSource != state.BaselineSource || current.BaselinePercent != tc.percent || current.BaselineCost != tc.cost {
				t.Fatal("a later sample replaced the explicit inception baseline")
			}
		})
	}
}

func TestOpenAIWeeklyFrozenEstimateExplicitBaselineRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC().Add(-time.Hour)
	reset := at.Add(7 * 24 * time.Hour)
	for _, tc := range []struct {
		name                string
		percent, cost       float64
		resetAt, observedAt time.Time
		identity, source    string
	}{
		{"missing identity", 20, 0, reset, at, "", "verified_history_inception"},
		{"blank identity", 20, 0, reset, at, " ", "verified_history_inception"},
		{"missing provenance", 20, 0, reset, at, "workspace-a", ""},
		{"missing window", 20, 0, time.Time{}, at, "workspace-a", "verified_history_inception"},
		{"missing observation", 20, 0, reset, time.Time{}, "workspace-a", "verified_history_inception"},
		{"observation at reset", 20, 0, reset, reset, "workspace-a", "verified_history_inception"},
		{"negative percent", -1, 0, reset, at, "workspace-a", "verified_history_inception"},
		{"nonfinite percent", math.NaN(), 0, reset, at, "workspace-a", "verified_history_inception"},
		{"negative cost", 20, -1, reset, at, "workspace-a", "verified_history_inception"},
		{"nonfinite cost", 20, math.Inf(1), reset, at, "workspace-a", "verified_history_inception"},
		{"terminal requires ordered observation", 100, 0, reset, at, "workspace-a", "verified_history_inception"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, ok := newOpenAIWeeklyFrozenEstimateStateFromBaseline(tc.percent, tc.cost, tc.resetAt, tc.identity, tc.observedAt, tc.source)
			if ok || state != (openAIWeeklyFrozenEstimateState{}) {
				t.Fatal("invalid baseline was accepted")
			}
		})
	}
}

func TestOpenAIWeeklyFrozenEstimateExplicitBaselineRetainsUnknownEvidence(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC().Add(-time.Hour)
	reset := at.Add(7 * 24 * time.Hour)
	unknown := newOpenAIWeeklyFrozenEstimateState(25, 120, reset, "workspace-a", at.Add(time.Minute))
	unknown.Mode, unknown.BaselineSource = openAIWeeklyEstimateModeUnknown, "legacy_start_unclassified_v13"
	extra := openAIWeeklyFrozenEstimateStateUpdate(unknown)
	legacyEvidence := map[string]any{"version": 13, "percent_bucket": 25, "snapshot_cost": 120.0}
	extra[openAIWeeklyEstimateBaselineKey].(map[string]any)["legacy_evidence"] = legacyEvidence
	before, err := json.Marshal(extra)
	if err != nil {
		t.Fatal(err)
	}
	seed, ok := newOpenAIWeeklyFrozenEstimateStateFromBaseline(20, 0, reset, "workspace-a", at, "verified_history_inception")
	if !ok {
		t.Fatal("verified inception was rejected")
	}
	updates := openAIWeeklyFrozenEstimateUpdateWithEvidence(seed, extra)
	after, err := json.Marshal(extra)
	if err != nil || string(before) != string(after) {
		t.Fatal("building explicit initialization updates mutated existing state")
	}
	raw := updates[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if !reflect.DeepEqual(raw["legacy_evidence"], legacyEvidence) {
		t.Fatal("original unclassified evidence was discarded")
	}
	previous, ok := raw["previous_sampling_baseline"].(map[string]any)
	if !ok || previous["baseline_percent"] != 25.0 || previous["baseline_source"] != unknown.BaselineSource {
		t.Fatal("later sampling baseline was relabeled as verified inception")
	}
}

func requireOpenAIWeeklyTwoRuleState(t *testing.T, account *Account) openAIWeeklyFrozenEstimateState {
	t.Helper()
	state, ok := readOpenAIWeeklyFrozenEstimateState(account.Extra)
	if !ok {
		t.Fatalf("invalid two-rule state: %#v", account.Extra[openAIWeeklyEstimateBaselineKey])
	}
	raw := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if parseExtraInt(raw["version"]) != openAIWeeklyFrozenEstimateTwoRuleStateVersion {
		t.Fatal("new state did not use version 15")
	}
	return state
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
	if math.Abs(state.BaselinePercent-float64(wantBaselinePercent)) > 1e-9 {
		t.Fatalf("baseline percent = %v, want %d", state.BaselinePercent, wantBaselinePercent)
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
