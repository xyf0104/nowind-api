package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type weeklyJoinRestoreRepo struct {
	*weeklyPersistenceCASRepo
	evidence *OpenAIWeeklyJoinEvidence
	err      error
	reads    int
}

func (r *weeklyJoinRestoreRepo) FindOpenAIWeeklyJoinEvidence(context.Context, *Account, time.Time) (*OpenAIWeeklyJoinEvidence, error) {
	r.reads++
	return r.evidence, r.err
}

func weeklyJoinRestoreFixture() (*AccountUsageService, *weeklyJoinRestoreRepo, *Account, *UsageProgress, time.Time) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	observed := now.Add(-time.Minute)
	reset := now.Add(3 * 24 * time.Hour)
	state := newOpenAIWeeklyFrozenEstimateState(41, 480, reset, "synthetic-join-identity", observed.Add(-time.Hour))
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		CreatedAt:   now.Add(-26 * time.Hour),
		Credentials: map[string]any{"chatgpt_account_id": state.Identity, "access_token": "synthetic"},
		Extra:       openAIWeeklyFrozenEstimateStateUpdate(state)}
	account.Extra["codex_usage_updated_at"] = observed.Format(time.RFC3339Nano)
	repo := &weeklyJoinRestoreRepo{
		weeklyPersistenceCASRepo: &weeklyPersistenceCASRepo{weeklyPersistenceLegacyRepo: &weeklyPersistenceLegacyRepo{}, applied: true},
		evidence: &OpenAIWeeklyJoinEvidence{AccountID: 42, Identity: state.Identity,
			Kind: OpenAIWeeklyJoinEvidenceImportedCorroboration, AuditID: 9,
			AuditCreatedAt: now.Add(-24 * time.Hour), ObservedAt: now.Add(-25 * time.Hour),
			BaselineObservedAt: now.Add(-27 * time.Hour),
			VerifiedAt:         now, ResetAt: reset, Percent: 11, Cost: 0},
	}
	progress := &UsageProgress{Utilization: 45, ResetsAt: &reset, WindowStats: &WindowStats{Cost: 853.5}}
	return &AccountUsageService{accountRepo: repo}, repo, account, progress, observed
}

func TestOpenAIWeeklyJoinRestoreUsesOriginalBaselineAndOriginalCAS(t *testing.T) {
	svc, repo, account, progress, observed := weeklyJoinRestoreFixture()
	svc.setOpenAIWeeklyFrozenEstimate(account, progress, 853.5, 860, true, observed, context.Background())
	require.NotNil(t, progress.WeeklyEstimateUSD)
	require.InDelta(t, 853.5/34*100, *progress.WeeklyEstimateUSD, 1e-8)
	require.Same(t, account, repo.expected)
	require.Equal(t, 1, repo.calls)
	require.Equal(t, 1, repo.reads)
	state, ok := readOpenAIWeeklyFrozenEstimateState(account.Extra)
	require.True(t, ok)
	require.Equal(t, 11.0, state.BaselinePercent)
	require.Equal(t, 0.0, state.BaselineCost)
	require.Equal(t, "verified_history_inception", state.BaselineSource)
	raw, ok := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	require.True(t, ok)
	require.Contains(t, raw, "join_evidence")
	require.Contains(t, raw, "previous_sampling_baseline")
	svc.setOpenAIWeeklyFrozenEstimate(account, progress, 855, 860, true, observed.Add(time.Second), context.Background())
	require.Equal(t, 1, repo.reads, "verified inception must not rescan or replace itself")
	require.InDelta(t, 853.5/34*100, *progress.WeeklyEstimateUSD, 1e-8, "freeze incomplete interval")
	raw, ok = account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	require.True(t, ok)
	require.Contains(t, raw, "join_evidence")
}

func TestOpenAIWeeklyJoinRestoreDoesNotPublishLostCAS(t *testing.T) {
	svc, repo, account, progress, observed := weeklyJoinRestoreFixture()
	repo.applied = false
	before, err := json.Marshal(account)
	require.NoError(t, err)
	svc.setOpenAIWeeklyFrozenEstimate(account, progress, 853.5, 860, true, observed, context.Background())
	require.Nil(t, progress.WeeklyEstimateUSD)
	after, err := json.Marshal(account)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestOpenAIWeeklyJoinRestoreRefusesMissingAndInvalidEvidence(t *testing.T) {
	for _, name := range []string{"missing", "read failure", "wrong identity", "wrong week", "nonzero cost", "future", "newer state", "before local creation", "wrong kind timestamp", "previous week observation"} {
		t.Run(name, func(t *testing.T) {
			svc, repo, account, progress, observed := weeklyJoinRestoreFixture()
			switch name {
			case "missing":
				repo.evidence = nil
			case "read failure":
				repo.err = errors.New("synthetic unavailable")
			case "wrong identity":
				repo.evidence.Identity = "other"
			case "wrong week":
				repo.evidence.ResetAt = repo.evidence.ResetAt.Add(7 * 24 * time.Hour)
			case "nonzero cost":
				repo.evidence.Cost = 1
			case "future":
				repo.evidence.ObservedAt = time.Now().Add(time.Hour)
			case "newer state":
				raw, ok := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
				require.True(t, ok)
				raw["observed_at"] = observed.Add(time.Second).Format(time.RFC3339Nano)
			case "before local creation":
				account.CreatedAt = repo.evidence.ObservedAt.Add(time.Second)
			case "wrong kind timestamp":
				repo.evidence.Kind = OpenAIWeeklyJoinEvidenceLocalInception
			case "previous week observation":
				repo.evidence.BaselineObservedAt = repo.evidence.ResetAt.Add(-8 * 24 * time.Hour)
			}
			svc.setOpenAIWeeklyFrozenEstimate(account, progress, 853.5, 860, true, observed, context.Background())
			require.Nil(t, progress.WeeklyEstimateUSD)
			if name == "missing" {
				state, ok := readOpenAIWeeklyFrozenEstimateState(account.Extra)
				require.True(t, ok)
				require.Equal(t, openAIWeeklyEstimateModeUnknown, state.Mode)
			} else {
				require.Zero(t, repo.calls)
			}
		})
	}
}

func TestOpenAIWeeklyJoinRestoreNeverReplacesRuleBOrExplicitRebase(t *testing.T) {
	for _, source := range []string{"observed_zero", "weekly_window_reset", "external_only_rebase", "cost_regression", "percent_regression"} {
		t.Run(source, func(t *testing.T) {
			svc, repo, account, progress, observed := weeklyJoinRestoreFixture()
			raw, ok := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
			require.True(t, ok)
			raw["baseline_source"] = source
			_, err := svc.accountWithVerifiedOpenAIWeeklyJoin(context.Background(), account, progress, 853.5, observed)
			require.NoError(t, err)
			require.Zero(t, repo.reads)
		})
	}
}

func TestOpenAIWeeklyJoinRestoreRetainsZeroStartRuleB(t *testing.T) {
	svc, repo, account, progress, observed := weeklyJoinRestoreFixture()
	state := newOpenAIWeeklyFrozenEstimateState(0, 0, *progress.ResetsAt,
		account.GetCredential("chatgpt_account_id"), observed.Add(-time.Hour))
	account.Extra[openAIWeeklyEstimateBaselineKey] = openAIWeeklyFrozenEstimateStateUpdate(state)[openAIWeeklyEstimateBaselineKey]
	progress.Utilization = 10
	svc.setOpenAIWeeklyFrozenEstimate(account, progress, 260, 265, true, observed, context.Background())
	require.Zero(t, repo.reads, "mid-join recovery must not replace an existing verified zero start")
	require.NotNil(t, progress.WeeklyEstimateUSD)
	require.InDelta(t, 260/0.09, *progress.WeeklyEstimateUSD, 1e-8)
	svc.setOpenAIWeeklyFrozenEstimate(account, progress, 265, 265, true, observed.Add(time.Second), context.Background())
	require.InDelta(t, 260/0.09, *progress.WeeklyEstimateUSD, 1e-8, "keep the old rule's bucket freeze")
}
