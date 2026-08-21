-- Migration 231 is the immutable schema baseline. Keep follow-up data repair
-- and soft-delete behavior append-only so future upgrades never need to edit
-- an applied migration.

-- PostgreSQL does not automatically index the referencing side of a foreign
-- key. This index prevents account cleanup from scanning the full detail table.
CREATE INDEX IF NOT EXISTS idx_user_group_account_allowlists_account_id
    ON user_group_account_allowlists (account_id);

-- XIASS users, groups, and accounts use deleted_at rather than physical DELETE.
-- Remove stale rows that predate the triggers below. Scope/detail DELETE
-- triggers installed by migration 231 publish transaction-bound invalidations.
DELETE FROM user_group_account_allowlist_scopes AS scope
USING users AS parent_user
WHERE scope.user_id = parent_user.id
  AND parent_user.deleted_at IS NOT NULL;

DELETE FROM user_group_account_allowlist_scopes AS scope
USING groups AS parent_group
WHERE scope.group_id = parent_group.id
  AND parent_group.deleted_at IS NOT NULL;

-- Deleting an account removes only that account selection. The restricted
-- scope deliberately remains, including when its detail set becomes empty.
DELETE FROM user_group_account_allowlists AS detail
USING accounts AS parent_account
WHERE detail.account_id = parent_account.id
  AND parent_account.deleted_at IS NOT NULL;

CREATE OR REPLACE FUNCTION cleanup_user_group_account_allowlists_on_soft_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    CASE TG_TABLE_NAME
        WHEN 'accounts' THEN
            EXECUTE format(
                'DELETE FROM %I.user_group_account_allowlists WHERE account_id = $1',
                TG_TABLE_SCHEMA
            ) USING NEW.id;
        WHEN 'users' THEN
            EXECUTE format(
                'DELETE FROM %I.user_group_account_allowlist_scopes WHERE user_id = $1',
                TG_TABLE_SCHEMA
            ) USING NEW.id;
        WHEN 'groups' THEN
            EXECUTE format(
                'DELETE FROM %I.user_group_account_allowlist_scopes WHERE group_id = $1',
                TG_TABLE_SCHEMA
            ) USING NEW.id;
        ELSE
            RAISE EXCEPTION 'unexpected allowlist soft-delete parent table: %.%',
                TG_TABLE_SCHEMA, TG_TABLE_NAME;
    END CASE;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_accounts_cleanup_user_group_account_allowlists
    ON accounts;
CREATE TRIGGER trg_accounts_cleanup_user_group_account_allowlists
AFTER UPDATE OF deleted_at ON accounts
FOR EACH ROW
WHEN (OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL)
EXECUTE FUNCTION cleanup_user_group_account_allowlists_on_soft_delete();

DROP TRIGGER IF EXISTS trg_users_cleanup_user_group_account_allowlists
    ON users;
CREATE TRIGGER trg_users_cleanup_user_group_account_allowlists
AFTER UPDATE OF deleted_at ON users
FOR EACH ROW
WHEN (OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL)
EXECUTE FUNCTION cleanup_user_group_account_allowlists_on_soft_delete();

DROP TRIGGER IF EXISTS trg_groups_cleanup_user_group_account_allowlists
    ON groups;
CREATE TRIGGER trg_groups_cleanup_user_group_account_allowlists
AFTER UPDATE OF deleted_at ON groups
FOR EACH ROW
WHEN (OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL)
EXECUTE FUNCTION cleanup_user_group_account_allowlists_on_soft_delete();

COMMENT ON FUNCTION cleanup_user_group_account_allowlists_on_soft_delete() IS
    'Maintains restricted account allowlists when XIASS parent rows are soft-deleted.';
