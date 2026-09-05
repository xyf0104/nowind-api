package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

const (
	openAIWeeklyFrozenEstimateStateVersion       = 14
	openAIWeeklyFrozenEstimateLegacyStateVersion = 13
)

// openAIWeeklyFrozenEstimateState keeps the first trustworthy XIASS cost and
// provider percentage seen after an account joins the current weekly window.
// Later estimates use only the local cost accumulated after that baseline.
type openAIWeeklyFrozenEstimateState struct {
	BaselinePercent int
	BaselineCost    float64
	PercentBucket   int
	SnapshotCost    float64
	EstimateUSD     float64
	HasEstimate     bool
	ResetAt         time.Time
	Identity        string
	ObservedAt      time.Time
}

// applyOpenAIWeeklyEstimate estimates the full weekly allowance from XIASS-only
// usage observed after the account joined. An account first seen at 20% / $0
// and later seen at 25% / $120 therefore estimates 120 / 5 * 100 = $2400.
func (s *AccountUsageService) applyOpenAIWeeklyEstimate(
	ctx context.Context,
	account *Account,
	progress *UsageProgress,
	currentStats *usagestats.AccountStats,
	now time.Time,
) {
	if s == nil || account == nil || progress == nil || progress.WindowStats == nil || currentStats == nil {
		return
	}

	currentCost := currentStats.Cost
	if !validOpenAIWeeklyEstimateValue(currentCost) || !validOpenAIWeeklyEstimateValue(progress.Utilization) {
		progress.WeeklyEstimateUSD = nil
		return
	}

	// A fully consumed official window has an exact local account total.
	if progress.Utilization >= 100-openAIWeeklyEstimateEpsilon {
		exact := currentCost
		progress.WeeklyEstimateUSD = &exact
		return
	}

	snapshotAt, snapshotOK := openAICodexSnapshotObservationAt(account, now)
	if !snapshotOK {
		s.setOpenAIWeeklyFrozenEstimate(account, progress, 0, currentCost, false, time.Time{}, ctx)
		return
	}

	rangeReader, ok := s.usageLogRepo.(accountWindowStatsRangeReader)
	if !ok {
		s.setOpenAIWeeklyFrozenEstimate(account, progress, 0, currentCost, false, snapshotAt, ctx)
		return
	}
	startAt := codexWindowStatsStart(progress, 7*24*time.Hour, snapshotAt)
	if !startAt.Before(snapshotAt) {
		s.setOpenAIWeeklyFrozenEstimate(account, progress, 0, currentCost, false, snapshotAt, ctx)
		return
	}

	boundedStats := s.cachedOpenAIWeeklyEstimateStats(account.ID, startAt, snapshotAt)
	if boundedStats == nil {
		stats, err := rangeReader.GetAccountWindowStatsRange(ctx, account.ID, startAt, snapshotAt)
		if err == nil && stats != nil && validOpenAIWeeklyEstimateValue(stats.Cost) {
			boundedStats = stats
			s.storeOpenAIWeeklyEstimateStats(account.ID, startAt, snapshotAt, stats)
		}
	}
	if boundedStats == nil {
		s.setOpenAIWeeklyFrozenEstimate(account, progress, 0, currentCost, false, snapshotAt, ctx)
		return
	}

	s.setOpenAIWeeklyFrozenEstimate(account, progress, boundedStats.Cost, currentCost, true, snapshotAt, ctx)
}

func (s *AccountUsageService) cachedOpenAIWeeklyEstimateStats(accountID int64, startAt, snapshotAt time.Time) *usagestats.AccountStats {
	if s == nil || s.cache == nil {
		return nil
	}
	cached, ok := s.cache.weeklyEstimateStatsCache.Load(accountID)
	if !ok {
		return nil
	}
	entry, ok := cached.(*weeklyEstimateStatsCache)
	if !ok || time.Since(entry.timestamp) >= windowStatsCacheTTL || !entry.startAt.Equal(startAt) || !entry.snapshotAt.Equal(snapshotAt) {
		return nil
	}
	return entry.stats
}

func (s *AccountUsageService) storeOpenAIWeeklyEstimateStats(accountID int64, startAt, snapshotAt time.Time, stats *usagestats.AccountStats) {
	if s == nil || s.cache == nil || stats == nil {
		return
	}
	s.cache.weeklyEstimateStatsCache.Store(accountID, &weeklyEstimateStatsCache{
		stats:      stats,
		startAt:    startAt,
		snapshotAt: snapshotAt,
		timestamp:  time.Now(),
	})
}

func (s *AccountUsageService) setOpenAIWeeklyFrozenEstimate(
	account *Account,
	progress *UsageProgress,
	snapshotCost, currentCost float64,
	snapshotMatched bool,
	observedAt time.Time,
	ctx context.Context,
) {
	estimate, updates := calculateOpenAIWeeklyFrozenEstimate(
		account,
		progress,
		snapshotCost,
		currentCost,
		snapshotMatched,
		observedAt,
	)
	progress.WeeklyEstimateUSD = estimate
	s.persistOpenAIWeeklyEstimate(ctx, account, updates)
}

func calculateOpenAIWeeklyFrozenEstimate(
	account *Account,
	progress *UsageProgress,
	snapshotCost, currentCost float64,
	snapshotMatched bool,
	observedAt time.Time,
) (*float64, map[string]any) {
	if account == nil || progress == nil || progress.WindowStats == nil ||
		!validOpenAIWeeklyEstimateValue(progress.Utilization) || !validOpenAIWeeklyEstimateValue(currentCost) {
		return nil, nil
	}

	percent := math.Max(0, math.Min(100, progress.Utilization))
	if percent >= 100-openAIWeeklyEstimateEpsilon {
		exact := currentCost
		return &exact, nil
	}

	identity := account.GetCredential("chatgpt_account_id")
	resetAt := time.Time{}
	if progress.ResetsAt != nil {
		resetAt = progress.ResetsAt.UTC()
	}
	bucket := openAIWeeklyEstimatePercentBucket(percent)
	state, stateOK := readOpenAIWeeklyFrozenEstimateState(account.Extra)
	stateNeedsPersist := false
	if !stateOK {
		// Version 13 divided the cumulative XIASS cost by the provider's total
		// percentage. Keep only its last aligned observation as a new baseline;
		// never inherit that incompatible estimate.
		state, stateOK = readOpenAIWeeklyFrozenEstimateLegacyState(account.Extra)
		stateNeedsPersist = stateOK
	}
	if stateOK && !state.matches(identity, resetAt, time.Now().UTC()) {
		stateOK = false
		stateNeedsPersist = false
	}

	if stateOK && !observedAt.IsZero() && !state.ObservedAt.IsZero() && observedAt.Before(state.ObservedAt) {
		return state.value(), openAIWeeklyFrozenEstimateMigrationUpdate(state, stateNeedsPersist)
	}

	if !snapshotMatched || !validOpenAIWeeklyEstimateValue(snapshotCost) ||
		snapshotCost > currentCost+openAIWeeklyEstimateEpsilon {
		if stateOK {
			return state.value(), openAIWeeklyFrozenEstimateMigrationUpdate(state, stateNeedsPersist)
		}
		return nil, nil
	}

	if !stateOK {
		state = newOpenAIWeeklyFrozenEstimateState(bucket, snapshotCost, resetAt, identity, observedAt)
		return nil, openAIWeeklyFrozenEstimateStateUpdate(state)
	}

	// A provider percentage regression starts a fresh sampling baseline. This
	// also handles a reset whose moving ETA remains close enough to the previous
	// ETA to look like the same active upstream window.
	if bucket < state.PercentBucket {
		state = newOpenAIWeeklyFrozenEstimateState(bucket, snapshotCost, resetAt, identity, observedAt)
		return nil, openAIWeeklyFrozenEstimateStateUpdate(state)
	}
	if bucket == state.PercentBucket && snapshotCost <= state.SnapshotCost+openAIWeeklyEstimateEpsilon {
		// Several aligned reads can land in the same displayed percentage. Keep
		// its maximum cumulative XIASS cost and ignore a smaller stale read.
		return state.value(), openAIWeeklyFrozenEstimateMigrationUpdate(state, stateNeedsPersist)
	}

	if bucket > state.PercentBucket && snapshotCost <= state.SnapshotCost+openAIWeeklyEstimateEpsilon {
		// The provider percentage advanced without new XIASS cost, so the usage
		// happened elsewhere. Rebase instead of mixing it into the local average.
		state = newOpenAIWeeklyFrozenEstimateState(bucket, snapshotCost, resetAt, identity, observedAt)
		return nil, openAIWeeklyFrozenEstimateStateUpdate(state)
	}

	changed := stateNeedsPersist || bucket != state.PercentBucket ||
		snapshotCost > state.SnapshotCost+openAIWeeklyEstimateEpsilon
	if !changed {
		return state.value(), nil
	}

	state.PercentBucket = bucket
	state.SnapshotCost = math.Max(state.SnapshotCost, snapshotCost)
	state.ResetAt = resetAt
	state.ObservedAt = observedAt.UTC()
	state.updateEstimate()
	return state.value(), openAIWeeklyFrozenEstimateStateUpdate(state)
}

func newOpenAIWeeklyFrozenEstimateState(bucket int, cost float64, resetAt time.Time, identity string, observedAt time.Time) openAIWeeklyFrozenEstimateState {
	return openAIWeeklyFrozenEstimateState{
		BaselinePercent: bucket,
		BaselineCost:    cost,
		PercentBucket:   bucket,
		SnapshotCost:    cost,
		ResetAt:         resetAt,
		Identity:        identity,
		ObservedAt:      observedAt.UTC(),
	}
}

func (state *openAIWeeklyFrozenEstimateState) updateEstimate() {
	if state == nil {
		return
	}
	deltaPercent := state.PercentBucket - state.BaselinePercent
	deltaCost := state.SnapshotCost - state.BaselineCost
	if deltaPercent <= 0 || deltaCost <= openAIWeeklyEstimateEpsilon {
		state.EstimateUSD = 0
		state.HasEstimate = false
		return
	}
	estimate := deltaCost / float64(deltaPercent) * 100
	state.EstimateUSD = estimate
	state.HasEstimate = validOpenAIWeeklyEstimateValue(estimate) && estimate > openAIWeeklyEstimateEpsilon
}

func openAIWeeklyEstimatePercentBucket(percent float64) int {
	if !validOpenAIWeeklyEstimateValue(percent) {
		return 0
	}
	return int(math.Floor(math.Max(0, math.Min(99, percent)) + openAIWeeklyEstimateEpsilon))
}

func (state openAIWeeklyFrozenEstimateState) matches(identity string, resetAt, now time.Time) bool {
	return state.BaselinePercent >= 0 && state.BaselinePercent < 100 &&
		state.PercentBucket >= state.BaselinePercent && state.PercentBucket < 100 &&
		validOpenAIWeeklyEstimateValue(state.BaselineCost) &&
		validOpenAIWeeklyEstimateValue(state.SnapshotCost) &&
		state.SnapshotCost+openAIWeeklyEstimateEpsilon >= state.BaselineCost &&
		state.Identity == identity && sameOpenAIWeeklyEstimateWindow(state.ResetAt, resetAt, now) &&
		(!state.HasEstimate || validOpenAIWeeklyEstimateValue(state.EstimateUSD))
}

func (state openAIWeeklyFrozenEstimateState) value() *float64 {
	if !state.HasEstimate || !validOpenAIWeeklyEstimateValue(state.EstimateUSD) || state.EstimateUSD <= openAIWeeklyEstimateEpsilon {
		return nil
	}
	estimate := state.EstimateUSD
	return &estimate
}

func openAIWeeklyFrozenEstimateStateUpdate(state openAIWeeklyFrozenEstimateState) map[string]any {
	raw := map[string]any{
		"version":             openAIWeeklyFrozenEstimateStateVersion,
		"baseline_percent":    state.BaselinePercent,
		"baseline_cost":       state.BaselineCost,
		"percent_bucket":      state.PercentBucket,
		"snapshot_cost":       state.SnapshotCost,
		"has_weekly_estimate": state.HasEstimate,
		"reset_at":            state.ResetAt.Format(time.RFC3339Nano),
		"identity":            state.Identity,
	}
	if !state.ObservedAt.IsZero() {
		raw["observed_at"] = state.ObservedAt.Format(time.RFC3339Nano)
	}
	if state.HasEstimate {
		raw["estimate_usd"] = state.EstimateUSD
	}
	return map[string]any{openAIWeeklyEstimateBaselineKey: raw}
}

func openAIWeeklyFrozenEstimateMigrationUpdate(state openAIWeeklyFrozenEstimateState, needed bool) map[string]any {
	if !needed {
		return nil
	}
	return openAIWeeklyFrozenEstimateStateUpdate(state)
}

func readOpenAIWeeklyFrozenEstimateState(extra map[string]any) (openAIWeeklyFrozenEstimateState, bool) {
	if extra == nil {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	raw, ok := extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if !ok || parseExtraInt(raw["version"]) != openAIWeeklyFrozenEstimateStateVersion {
		return openAIWeeklyFrozenEstimateState{}, false
	}

	baselinePercentValue, baselinePercentOK := parseOpenAIWeeklyEstimateNumber(raw, "baseline_percent")
	baselineCost, baselineCostOK := parseOpenAIWeeklyEstimateNumber(raw, "baseline_cost")
	bucketValue, bucketOK := parseOpenAIWeeklyEstimateNumber(raw, "percent_bucket")
	snapshotCost, costOK := parseOpenAIWeeklyEstimateNumber(raw, "snapshot_cost")
	hasEstimate, hasEstimateOK := parseOpenAIWeeklyEstimateBool(raw, "has_weekly_estimate")
	resetText, resetOK := raw["reset_at"].(string)
	identity, identityOK := raw["identity"].(string)
	if !baselinePercentOK || !baselineCostOK || !bucketOK || !costOK || !hasEstimateOK || !resetOK || !identityOK ||
		math.Trunc(baselinePercentValue) != baselinePercentValue || math.Trunc(bucketValue) != bucketValue {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	resetAt, err := parseTime(resetText)
	if err != nil {
		return openAIWeeklyFrozenEstimateState{}, false
	}

	state := openAIWeeklyFrozenEstimateState{
		BaselinePercent: int(baselinePercentValue),
		BaselineCost:    baselineCost,
		PercentBucket:   int(bucketValue),
		SnapshotCost:    snapshotCost,
		HasEstimate:     hasEstimate,
		ResetAt:         resetAt.UTC(),
		Identity:        identity,
	}
	if rawObservedAt, exists := raw["observed_at"]; exists && rawObservedAt != nil {
		observedAt, observedErr := parseTime(fmt.Sprint(rawObservedAt))
		if observedErr != nil {
			return openAIWeeklyFrozenEstimateState{}, false
		}
		state.ObservedAt = observedAt.UTC()
	}
	if state.HasEstimate {
		estimate, estimateOK := parseOpenAIWeeklyEstimateNumber(raw, "estimate_usd")
		if !estimateOK {
			return openAIWeeklyFrozenEstimateState{}, false
		}
		state.EstimateUSD = estimate
	}
	return state, state.matches(state.Identity, state.ResetAt, time.Now().UTC())
}

func readOpenAIWeeklyFrozenEstimateLegacyState(extra map[string]any) (openAIWeeklyFrozenEstimateState, bool) {
	if extra == nil {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	raw, ok := extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if !ok || parseExtraInt(raw["version"]) != openAIWeeklyFrozenEstimateLegacyStateVersion {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	bucketValue, bucketOK := parseOpenAIWeeklyEstimateNumber(raw, "percent_bucket")
	snapshotCost, costOK := parseOpenAIWeeklyEstimateNumber(raw, "snapshot_cost")
	resetText, resetOK := raw["reset_at"].(string)
	identity, identityOK := raw["identity"].(string)
	if !bucketOK || !costOK || !resetOK || !identityOK || math.Trunc(bucketValue) != bucketValue {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	resetAt, err := parseTime(resetText)
	if err != nil {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	bucket := int(bucketValue)
	state := newOpenAIWeeklyFrozenEstimateState(bucket, snapshotCost, resetAt.UTC(), identity, time.Time{})
	if rawObservedAt, exists := raw["observed_at"]; exists && rawObservedAt != nil {
		observedAt, observedErr := parseTime(fmt.Sprint(rawObservedAt))
		if observedErr != nil {
			return openAIWeeklyFrozenEstimateState{}, false
		}
		state.ObservedAt = observedAt.UTC()
	}
	return state, state.matches(state.Identity, state.ResetAt, time.Now().UTC())
}
