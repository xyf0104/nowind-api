package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserGroupAccountAllowlistMigrationPersistsRestrictedScopeIndependently(t *testing.T) {
	content, err := FS.ReadFile("231_user_group_account_allowlists.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(strings.ToLower(string(content))), " ")
	scopeDDL := "create table if not exists user_group_account_allowlist_scopes"
	detailDDL := "create table if not exists user_group_account_allowlists"
	require.Contains(t, sql, scopeDDL)
	require.Contains(t, sql, detailDDL)
	require.Less(t, strings.Index(sql, scopeDDL), strings.Index(sql, detailDDL), "scope table must exist before detail foreign keys")
	require.Contains(t, sql, "primary key (user_id, group_id)")
	require.Contains(t, sql, "foreign key (user_id, group_id) references user_group_account_allowlist_scopes(user_id, group_id) on delete cascade")
	require.Contains(t, sql, "foreign key (account_id) references accounts(id) on delete cascade")
	require.Contains(t, sql, "create index if not exists idx_user_group_account_allowlists_account_id on user_group_account_allowlists (account_id)")

	scopeBlockEnd := strings.Index(sql, detailDDL)
	require.NotContains(t, sql[:scopeBlockEnd], "references accounts", "account deletion must remove details without removing the restricted scope")
}

func TestUserGroupAccountAllowlistMigrationBackfillsUnrecordedDraftAndInvalidatesInstances(t *testing.T) {
	content, err := FS.ReadFile("231_user_group_account_allowlists.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(strings.ToLower(string(content))), " ")
	require.Contains(t, sql, "insert into user_group_account_allowlist_scopes (user_id, group_id, created_at, updated_at) select user_id, group_id, min(created_at), max(updated_at) from user_group_account_allowlists group by user_id, group_id on conflict (user_id, group_id) do nothing")
	require.Contains(t, sql, "if not exists ( select 1 from pg_constraint where conname = 'user_group_account_allowlists_scope_fkey'")
	require.Contains(t, sql, "add constraint user_group_account_allowlists_scope_fkey")
	require.Contains(t, sql, "not valid")
	require.Contains(t, sql, "validate constraint user_group_account_allowlists_scope_fkey")

	require.Contains(t, sql, "create or replace function notify_user_group_account_allowlist_change()")
	require.Contains(t, sql, "pg_notify( 'xiass_user_group_account_allowlist'")
	require.Contains(t, sql, "trg_user_group_account_allowlist_scopes_notify")
	require.Contains(t, sql, "trg_user_group_account_allowlists_notify")
	require.Contains(t, sql, "a recorded checksum mismatch must be repaired from the exact original sql")
}

func TestUserGroupAccountAllowlistSoftDeleteMigrationIsAppendOnlyAndTransactional(t *testing.T) {
	content, err := FS.ReadFile("233_user_group_account_allowlist_soft_delete.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(strings.ToLower(string(content))), " ")
	require.Contains(t, sql, "create index if not exists idx_user_group_account_allowlists_account_id on user_group_account_allowlists (account_id)")
	require.Contains(t, sql, "delete from user_group_account_allowlist_scopes as scope using users as parent_user")
	require.Contains(t, sql, "scope.user_id = parent_user.id and parent_user.deleted_at is not null")
	require.Contains(t, sql, "delete from user_group_account_allowlist_scopes as scope using groups as parent_group")
	require.Contains(t, sql, "scope.group_id = parent_group.id and parent_group.deleted_at is not null")
	require.Contains(t, sql, "delete from user_group_account_allowlists as detail using accounts as parent_account")
	require.Contains(t, sql, "detail.account_id = parent_account.id and parent_account.deleted_at is not null")

	require.Contains(t, sql, "create or replace function cleanup_user_group_account_allowlists_on_soft_delete()")
	require.Contains(t, sql, "delete from %i.user_group_account_allowlists where account_id = $1")
	require.Contains(t, sql, "delete from %i.user_group_account_allowlist_scopes where user_id = $1")
	require.Contains(t, sql, "delete from %i.user_group_account_allowlist_scopes where group_id = $1")
	require.Contains(t, sql, "when (old.deleted_at is null and new.deleted_at is not null)")
	require.Contains(t, sql, "trg_accounts_cleanup_user_group_account_allowlists")
	require.Contains(t, sql, "trg_users_cleanup_user_group_account_allowlists")
	require.Contains(t, sql, "trg_groups_cleanup_user_group_account_allowlists")

	initialContent, err := FS.ReadFile("231_user_group_account_allowlists.sql")
	require.NoError(t, err)
	initialSQL := strings.Join(strings.Fields(strings.ToLower(string(initialContent))), " ")
	require.Contains(t, initialSQL, "after insert or update or delete on user_group_account_allowlist_scopes")
	require.Contains(t, initialSQL, "after insert or update or delete on user_group_account_allowlists")
	require.Contains(t, initialSQL, "pg_notify( 'xiass_user_group_account_allowlist'", "cleanup DELETEs must reuse transaction-bound PostgreSQL notifications")
}
