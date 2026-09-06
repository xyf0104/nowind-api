//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIQuotaFullEditPreservesNewerCASState(t *testing.T) {
	ctx := context.Background()
	credentials := map[string]any{"chatgpt_account_id": "synthetic-workspace", "chatgpt_user_id": "synthetic-user", "access_token": "synthetic-original"}
	extra := weeklyStatePGExtra()
	extra["codex_7d_used_percent"] = 20.0
	extra["codex_5h_used_percent"] = 4.0
	extra["unrelated"] = "preserved"
	a := weeklyStatePGAccount(t, credentials, extra)
	repo := NewAccountRepository(integrationEntClient, integrationDB, nil)
	stale, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	patch := map[string]any{openAIWeeklyStateBaselineKey: map[string]any{"test_maximum": 50.0}}
	applied, err := repo.(service.OpenAIWeeklyStateRepository).CompareAndSwapOpenAIWeeklyState(ctx, a, patch)
	require.NoError(t, err)
	require.True(t, applied)
	stale.Name = "edited display name"
	stale.Credentials["access_token"] = "synthetic-renewed"
	stale.Extra["custom_setting"] = true
	require.NoError(t, repo.Update(ctx, stale))
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, patch[openAIWeeklyStateBaselineKey], got.Extra[openAIWeeklyStateBaselineKey])
	require.Equal(t, "test-epoch", got.Extra[openAIWeeklyStateEpochKey])
	require.Equal(t, 20.0, got.Extra["codex_7d_used_percent"])
	require.Equal(t, 4.0, got.Extra["codex_5h_used_percent"])
	require.Equal(t, "preserved", got.Extra["unrelated"])
	require.Equal(t, true, got.Extra["custom_setting"])
	require.Equal(t, "synthetic-renewed", got.GetCredential("access_token"))
	require.Equal(t, "edited display name", got.Name)
}

func TestOpenAIQuotaIdentityChangeClearsOnlyQuotaRuntime(t *testing.T) {
	for _, field := range []string{"chatgpt_account_id", "chatgpt_user_id"} {
		t.Run(field, func(t *testing.T) {
			ctx := context.Background()
			extra := weeklyStatePGExtra()
			extra["codex_7d_used_percent"] = 33.0
			extra["codex_5h_used_percent"] = 12.0
			extra["codex_reset_credit_remaining"] = 2.0
			extra["codex_fingerprint_seed"] = "synthetic-preserved-seed"
			extra["codex_fingerprint_mode"] = "off"
			extra["unrelated"] = "keep"
			a := weeklyStatePGAccount(t, map[string]any{"chatgpt_account_id": "workspace-a", "chatgpt_user_id": "user-a"}, extra)
			repo := NewAccountRepository(integrationEntClient, integrationDB, nil)
			changed, err := repo.GetByID(ctx, a.ID)
			require.NoError(t, err)
			changed.Credentials[field] = "different-identity"
			require.NoError(t, repo.Update(ctx, changed))
			got, err := repo.GetByID(ctx, a.ID)
			require.NoError(t, err)
			for key := range got.Extra {
				require.False(t, openAIQuotaManagedExtraKey(key), "old identity's quota key survived: %s", key)
			}
			require.Equal(t, "synthetic-preserved-seed", got.Extra["codex_fingerprint_seed"])
			require.Equal(t, "off", got.Extra["codex_fingerprint_mode"])
			require.Equal(t, "keep", got.Extra["unrelated"])
		})
	}
}

func TestOpenAIQuotaFullEditDoesNotResurrectMissingState(t *testing.T) {
	ctx := context.Background()
	a := weeklyStatePGAccount(t, map[string]any{"chatgpt_account_id": "synthetic-workspace"}, map[string]any{})
	repo := NewAccountRepository(integrationEntClient, integrationDB, nil)
	stale, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	stale.Extra[openAIWeeklyStateBaselineKey] = map[string]any{"invented": true}
	stale.Extra[openAIWeeklyStateEpochKey] = "invented"
	stale.Extra["codex_7d_used_percent"] = 55.0
	require.NoError(t, repo.Update(ctx, stale))
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.NotContains(t, got.Extra, openAIWeeklyStateBaselineKey)
	require.NotContains(t, got.Extra, openAIWeeklyStateEpochKey)
	require.NotContains(t, got.Extra, "codex_7d_used_percent")
}
