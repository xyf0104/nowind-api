package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewGroupAccountsDefaultAllowedMigration(t *testing.T) {
	content, err := FS.ReadFile("234_new_group_accounts_default_allowed.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(strings.ToLower(string(content))), " ")
	require.Contains(t, sql, "membership.created_at >= scope.updated_at")
	require.Contains(t, sql, "create or replace function allow_new_group_account_for_restricted_users()")
	require.Contains(t, sql, "after insert on account_groups")
	require.Contains(t, sql, "insert into user_group_account_allowlists (user_id, group_id, account_id)")
	require.Contains(t, sql, "on conflict (user_id, group_id, account_id) do nothing")
}
