package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstream169PasskeyMigrationAppendsAfterReleasedXIASSMigrations(t *testing.T) {
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)

	positions := make(map[string]int, len(entries))
	for i, entry := range entries {
		positions[entry.Name()] = i
	}

	require.Contains(t, positions, "192_add_users_email_alias_dedup_index_notx.sql")
	require.Contains(t, positions, "193_passkey_credentials.sql")
	require.Less(
		t,
		positions["192_add_users_email_alias_dedup_index_notx.sql"],
		positions["193_passkey_credentials.sql"],
	)
	require.NotContains(t, positions, "191_passkey_credentials.sql")
}
