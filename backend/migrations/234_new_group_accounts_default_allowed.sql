-- Accounts newly added to a group remain enabled for every user who has an
-- explicit account allowlist for that group. Existing manual exclusions stay
-- excluded because unchanged account-group rows are no longer recreated.

INSERT INTO user_group_account_allowlists (user_id, group_id, account_id)
SELECT scope.user_id, scope.group_id, membership.account_id
FROM user_group_account_allowlist_scopes AS scope
JOIN account_groups AS membership
  ON membership.group_id = scope.group_id
 AND membership.created_at >= scope.updated_at
ON CONFLICT (user_id, group_id, account_id) DO NOTHING;

CREATE OR REPLACE FUNCTION allow_new_group_account_for_restricted_users()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO user_group_account_allowlists (user_id, group_id, account_id)
    SELECT scope.user_id, scope.group_id, NEW.account_id
    FROM user_group_account_allowlist_scopes AS scope
    WHERE scope.group_id = NEW.group_id
    ON CONFLICT (user_id, group_id, account_id) DO NOTHING;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_account_groups_default_allowlist
    ON account_groups;
CREATE TRIGGER trg_account_groups_default_allowlist
AFTER INSERT ON account_groups
FOR EACH ROW EXECUTE FUNCTION allow_new_group_account_for_restricted_users();

COMMENT ON FUNCTION allow_new_group_account_for_restricted_users() IS
    'Adds a newly grouped account to existing restricted user allowlists without restoring prior manual exclusions.';
