package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type weeklyPersistenceLegacyRepo struct {
	AccountRepository
	writes int
}

func (r *weeklyPersistenceLegacyRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	r.writes++
	return nil
}

type weeklyPersistenceCASRepo struct {
	*weeklyPersistenceLegacyRepo
	applied  bool
	err      error
	expected *Account
	updates  map[string]any
	calls    int
}

func (r *weeklyPersistenceCASRepo) CompareAndSwapOpenAIWeeklyState(_ context.Context, expected *Account, updates map[string]any) (bool, error) {
	r.calls++
	r.expected = expected
	r.updates = updates
	return r.applied, r.err
}

func TestWeeklyEstimatePersistenceRequiresCAS(t *testing.T) {
	account := &Account{ID: 42, Extra: map[string]any{"unrelated": true}}
	updates := map[string]any{openAIWeeklyEstimateBaselineKey: map[string]any{"version": 14}}
	legacy := &weeklyPersistenceLegacyRepo{}
	svc := &AccountUsageService{accountRepo: legacy}
	require.False(t, svc.persistOpenAIWeeklyEstimate(context.Background(), account, updates))
	require.Zero(t, legacy.writes, "never fall back to unconditional Extra replacement")
	require.NotContains(t, account.Extra, openAIWeeklyEstimateBaselineKey)
}

func TestWeeklyEstimatePersistencePublishesOnlySuccessfulCAS(t *testing.T) {
	for _, tc := range []struct {
		name    string
		applied bool
		err     error
	}{
		{name: "accepted", applied: true},
		{name: "stale"},
		{name: "database failure", err: errors.New("synthetic database unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := map[string]any{"version": 14, "snapshot_cost": 20.0}
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "synthetic-only"},
				Extra:       map[string]any{"unrelated": true, openAIWeeklyEstimateBaselineKey: before}}
			updates := map[string]any{openAIWeeklyEstimateBaselineKey: map[string]any{"version": 14, "snapshot_cost": 25.0}}
			repo := &weeklyPersistenceCASRepo{weeklyPersistenceLegacyRepo: &weeklyPersistenceLegacyRepo{}, applied: tc.applied, err: tc.err}
			svc := &AccountUsageService{accountRepo: repo}
			require.Equal(t, tc.applied, svc.persistOpenAIWeeklyEstimate(context.Background(), account, updates))
			require.Same(t, account, repo.expected)
			require.Equal(t, updates, repo.updates)
			require.Equal(t, 1, repo.calls)
			require.Zero(t, repo.writes)
			require.Equal(t, true, account.Extra["unrelated"])
			if tc.applied {
				require.Equal(t, updates[openAIWeeklyEstimateBaselineKey], account.Extra[openAIWeeklyEstimateBaselineKey])
			} else {
				require.Equal(t, before, account.Extra[openAIWeeklyEstimateBaselineKey])
			}
		})
	}
}

func TestWeeklyEstimatePersistenceNoMutationIsAlreadyAccepted(t *testing.T) {
	var svc *AccountUsageService
	require.True(t, svc.persistOpenAIWeeklyEstimate(context.Background(), nil, nil))
	require.False(t, svc.persistOpenAIWeeklyEstimate(context.Background(), nil, map[string]any{openAIWeeklyEstimateBaselineKey: nil}))
}

func TestWeeklyQuotaRuntimeFieldsCannotBeWrittenByAccountExtraForm(t *testing.T) {
	updates := map[string]any{
		"codex_7d_estimate_baseline":     map[string]any{"invented": true},
		"codex_7d_estimate_epoch":        "invented",
		"codex_usage_updated_at":         "2099-01-01T00:00:00Z",
		"codex_7d_used_percent":          1.0,
		"codex_5h_used_percent":          1.0,
		"codex_primary_window_minutes":   1,
		"codex_secondary_window_minutes": 1,
		"codex_reset_credit_remaining":   99,
		"codex_fingerprint_mode":         "off",
		"custom":                         true,
	}
	clean := stripOpenAIQuotaRuntimeExtra(updates)
	require.Equal(t, map[string]any{"codex_fingerprint_mode": "off", "custom": true}, clean)
	require.Len(t, updates, 10, "sanitizing a form must not mutate the input snapshot")
	for key := range clean {
		require.False(t, IsOpenAIQuotaRuntimeExtraKey(key))
	}

	// All discarded fields can be removed before account access or persistence.
	repo := &weeklyPersistenceLegacyRepo{}
	svc := &adminServiceImpl{accountRepo: repo}
	require.NoError(t, svc.UpdateAccountExtra(context.Background(), 42,
		map[string]any{openAIWeeklyEstimateBaselineKey: "forged", "codex_7d_estimate_epoch": "forged"}))
	require.Zero(t, repo.writes)
}

func TestWeeklyPassiveFullQuotaCannotBypassObservationOrdering(t *testing.T) {
	svc, account, stats := readOnlyUsageFixture()
	newer := time.Now().UTC().Add(-time.Minute)
	account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)["observed_at"] = newer.Format(time.RFC3339Nano)
	account.Extra["codex_7d_used_percent"] = 100.0
	usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.NotNil(t, usage.SevenDay)
	require.Nil(t, usage.SevenDay.WeeklyEstimateUSD, "an older 100%% snapshot must not override newer state")
	require.Equal(t, stats.stats.Cost, usage.SevenDay.WindowStats.Cost, "actual usage remains visible")
}
