package service

import (
	"context"
	"fmt"
	"maps"
	"time"
)

// Recovery is staged on a copy. The caller commits the resulting estimate with
// the original account snapshot's CAS, never with this historical snapshot.
func (s *AccountUsageService) accountWithVerifiedOpenAIWeeklyJoin(ctx context.Context, account *Account, progress *UsageProgress, snapshotCost float64, observedAt time.Time) (*Account, error) {
	reader, ok := s.accountRepo.(OpenAIWeeklyJoinEvidenceRepository)
	if !ok || account == nil || !account.IsOpenAIOAuth() || account.IsShadow() ||
		progress == nil || progress.ResetsAt == nil || observedAt.IsZero() ||
		!validOpenAIWeeklyEstimateValue(snapshotCost) || progress.Utilization >= 100 {
		return account, nil
	}
	now := time.Now().UTC()
	resetAt := progress.ResetsAt.UTC()
	identity := account.GetCredential("chatgpt_account_id")
	if identity == "" || !resetAt.After(now) || observedAt.After(now.Add(5*time.Second)) {
		return account, nil
	}
	raw, _ := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if parseExtraInt(raw["version"]) > openAIWeeklyFrozenEstimateTwoRuleStateVersion {
		return account, nil
	}
	state, valid := readOpenAIWeeklyFrozenEstimateState(account.Extra)
	if !valid {
		state, valid = readOpenAIWeeklyFrozenEstimateLegacyState(account.Extra)
	}
	if valid {
		if !state.ObservedAt.IsZero() && (observedAt.Before(state.ObservedAt) ||
			(observedAt.Equal(state.ObservedAt) && progress.Utilization != state.SnapshotPercent)) {
			return account, nil
		}
		if !state.matches(identity, resetAt, now) {
			// A genuine new window or identity is handled by the calculator.
			return account, nil
		}
		if progress.Utilization < state.SnapshotPercent || snapshotCost < state.SnapshotCost {
			return account, nil
		}
		switch state.BaselineSource {
		case "observed_zero", "weekly_window_reset", "verified_history_inception", "pre_create_quota_read",
			"external_only_rebase", "percent_regression", "cost_regression", "terminal_observation":
			return account, nil
		}
	}
	if progress.Utilization == 0 {
		return account, nil
	}
	evidence, err := reader.FindOpenAIWeeklyJoinEvidence(ctx, account, resetAt)
	if err != nil {
		return nil, fmt.Errorf("resolve weekly join evidence: %w", err)
	}
	var seed openAIWeeklyFrozenEstimateState
	if evidence != nil {
		if !validOpenAIWeeklyJoinForObservation(evidence, account, progress, snapshotCost, observedAt, now) {
			return nil, fmt.Errorf("weekly join evidence does not match the current observation")
		}
		var seeded bool
		seed, seeded = newOpenAIWeeklyFrozenEstimateStateFromBaseline(evidence.Percent, evidence.Cost, resetAt, identity, evidence.ObservedAt, "verified_history_inception")
		if !seeded {
			return nil, fmt.Errorf("weekly join evidence cannot establish a baseline")
		}
	} else {
		if valid && state.Mode == openAIWeeklyEstimateModeUnknown {
			return account, nil
		}
		// A later first read is not proof of joining at that percentage.
		seed = newOpenAIWeeklyFrozenEstimateState(progress.Utilization, snapshotCost, resetAt, identity, observedAt)
		seed.Mode, seed.BaselineSource = openAIWeeklyEstimateModeUnknown, "awaiting_verified_join"
	}
	updates := openAIWeeklyFrozenEstimateUpdateWithEvidence(seed, account.Extra)
	if evidence != nil {
		baseline, ok := updates[openAIWeeklyEstimateBaselineKey].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("weekly join baseline could not be constructed")
		}
		baseline["join_evidence"] = map[string]any{
			"kind": string(evidence.Kind), "audit_id": evidence.AuditID,
			"percent": evidence.Percent, "cost": evidence.Cost, "identity": evidence.Identity,
			"observed_at": evidence.ObservedAt.UTC().Format(time.RFC3339Nano),
			"reset_at":    evidence.ResetAt.UTC().Format(time.RFC3339Nano),
		}
	}
	copy := *account
	copy.Extra = maps.Clone(account.Extra)
	mergeAccountExtra(&copy, updates)
	return &copy, nil
}

func validOpenAIWeeklyJoinForObservation(e *OpenAIWeeklyJoinEvidence, account *Account, progress *UsageProgress, cost float64, observedAt, now time.Time) bool {
	if e == nil || e.AccountID != account.ID || e.Identity != account.GetCredential("chatgpt_account_id") ||
		e.AuditID <= 0 || e.Cost != 0 || !validOpenAIWeeklyEstimateValue(e.Percent) ||
		e.Percent > 99 || e.Percent > progress.Utilization || cost < e.Cost ||
		e.ObservedAt.IsZero() || e.ObservedAt.After(observedAt) || e.AuditCreatedAt.Before(e.ObservedAt) ||
		e.AuditCreatedAt.After(now.Add(5*time.Second)) || e.VerifiedAt.Before(e.AuditCreatedAt) ||
		e.VerifiedAt.After(now.Add(5*time.Second)) || progress.ResetsAt == nil ||
		e.ResetAt.IsZero() || e.ResetAt.Sub(*progress.ResetsAt).Abs() > time.Minute ||
		account.CreatedAt.IsZero() || e.ObservedAt.Before(account.CreatedAt) ||
		!e.ObservedAt.Before(e.ResetAt) || e.ObservedAt.Before(e.ResetAt.Add(-7*24*time.Hour-time.Minute)) ||
		e.BaselineObservedAt.IsZero() || e.BaselineObservedAt.After(e.ObservedAt) ||
		e.BaselineObservedAt.Before(e.ResetAt.Add(-7*24*time.Hour-time.Minute)) {
		return false
	}
	switch e.Kind {
	case OpenAIWeeklyJoinEvidenceLocalInception:
		return e.BaselineObservedAt.Equal(e.ObservedAt)
	case OpenAIWeeklyJoinEvidenceImportedCorroboration:
		return e.BaselineObservedAt.Before(account.CreatedAt)
	default:
		return false
	}
}
