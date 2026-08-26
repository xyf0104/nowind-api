package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

const openAIWeeklyFrozenEstimateStateVersion = 13

// openAIWeeklyFrozenEstimateState stores one immutable value for each integer
// provider percentage. The frozen cost is the XIASS account cost at the exact
// official quota observation, not a later live window total.
type openAIWeeklyFrozenEstimateState struct {
	PercentBucket int
	SnapshotCost  float64
	EstimateUSD   float64
	HasEstimate   bool
	ResetAt       time.Time
	Identity      string
	ObservedAt    time.Time
}

// applyOpenAIWeeklyEstimate implements the administrator-selected percentage
// bucket rule: when the official 7d window first reaches P%, freeze the local
// account cost at that observation and calculate cost / (P-1)%. The displayed
// result remains unchanged throughout P% and changes only after P+1% arrives.
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

	// A fully consumed official window has an exact local account total. It is
	// not an estimate and must not use the P-1 denominator.
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
	if stateOK && !state.matches(identity, resetAt, time.Now().UTC()) {
		stateOK = false
	}

	if stateOK {
		// A delayed response must not roll a newer bucket backward. The caller
		// keeps the immutable result that was already persisted for this window.
		if !observedAt.IsZero() && !state.ObservedAt.IsZero() && observedAt.Before(state.ObservedAt) {
			return state.value(), nil
		}
		if bucket == state.PercentBucket {
			return state.value(), nil
		}
		if bucket < state.PercentBucket {
			if !snapshotMatched || observedAt.IsZero() {
				return state.value(), nil
			}
			stateOK = false
		} else if !snapshotMatched || !validOpenAIWeeklyEstimateValue(snapshotCost) ||
			snapshotCost > currentCost+openAIWeeklyEstimateEpsilon {
			return state.value(), nil
		}
	}

	if !snapshotMatched || !validOpenAIWeeklyEstimateValue(snapshotCost) ||
		snapshotCost > currentCost+openAIWeeklyEstimateEpsilon {
		return nil, nil
	}

	state = openAIWeeklyFrozenEstimateState{
		PercentBucket: bucket,
		SnapshotCost:  snapshotCost,
		ResetAt:       resetAt,
		Identity:      identity,
		ObservedAt:    observedAt.UTC(),
	}
	if bucket >= 2 && snapshotCost > openAIWeeklyEstimateEpsilon {
		previousPercent := float64(bucket-1) / 100
		state.EstimateUSD = snapshotCost / previousPercent
		state.HasEstimate = validOpenAIWeeklyEstimateValue(state.EstimateUSD)
	}
	return state.value(), openAIWeeklyFrozenEstimateStateUpdate(state)
}

func openAIWeeklyEstimatePercentBucket(percent float64) int {
	if !validOpenAIWeeklyEstimateValue(percent) {
		return 0
	}
	return int(math.Floor(math.Max(0, math.Min(99, percent)) + openAIWeeklyEstimateEpsilon))
}

func (state openAIWeeklyFrozenEstimateState) matches(identity string, resetAt, now time.Time) bool {
	return state.PercentBucket >= 0 && state.PercentBucket < 100 &&
		validOpenAIWeeklyEstimateValue(state.SnapshotCost) && state.Identity == identity &&
		sameOpenAIWeeklyEstimateWindow(state.ResetAt, resetAt, now) &&
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

func readOpenAIWeeklyFrozenEstimateState(extra map[string]any) (openAIWeeklyFrozenEstimateState, bool) {
	if extra == nil {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	raw, ok := extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if !ok || parseExtraInt(raw["version"]) != openAIWeeklyFrozenEstimateStateVersion {
		return openAIWeeklyFrozenEstimateState{}, false
	}

	bucketValue, bucketOK := parseOpenAIWeeklyEstimateNumber(raw, "percent_bucket")
	snapshotCost, costOK := parseOpenAIWeeklyEstimateNumber(raw, "snapshot_cost")
	hasEstimate, hasEstimateOK := parseOpenAIWeeklyEstimateBool(raw, "has_weekly_estimate")
	resetText, resetOK := raw["reset_at"].(string)
	identity, identityOK := raw["identity"].(string)
	if !bucketOK || !costOK || !hasEstimateOK || !resetOK || !identityOK || math.Trunc(bucketValue) != bucketValue {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	resetAt, err := parseTime(resetText)
	if err != nil {
		return openAIWeeklyFrozenEstimateState{}, false
	}

	state := openAIWeeklyFrozenEstimateState{
		PercentBucket: int(bucketValue),
		SnapshotCost:  snapshotCost,
		HasEstimate:   hasEstimate,
		ResetAt:       resetAt.UTC(),
		Identity:      identity,
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
