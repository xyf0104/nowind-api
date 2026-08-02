package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstream170ProfitControlMigrationsAppendAfterReleasedXIASSMigrations(t *testing.T) {
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)

	positions := make(map[string]int, len(entries))
	for i, entry := range entries {
		positions[entry.Name()] = i
	}

	require.Contains(t, positions, "193_passkey_credentials.sql")
	require.Contains(t, positions, "194_group_profit_control.sql")
	require.Contains(t, positions, "195_group_profit_control_auth_cache_invalidation.sql")
	require.Less(
		t,
		positions["193_passkey_credentials.sql"],
		positions["194_group_profit_control.sql"],
	)
	require.Less(
		t,
		positions["194_group_profit_control.sql"],
		positions["195_group_profit_control_auth_cache_invalidation.sql"],
	)
	require.NotContains(t, positions, "192_group_profit_control.sql")
	require.NotContains(t, positions, "193_group_profit_control_auth_cache_invalidation.sql")
}
