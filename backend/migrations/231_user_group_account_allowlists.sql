-- A scope row is the durable restricted marker. Detail rows may be empty, so
-- deleting the final selected account never restores unrestricted scheduling.
CREATE TABLE IF NOT EXISTS user_group_account_allowlist_scopes (
    user_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, group_id),
    CONSTRAINT user_group_account_allowlist_scopes_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT user_group_account_allowlist_scopes_group_id_fkey
        FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_group_account_allowlist_scopes_group_user
    ON user_group_account_allowlist_scopes (group_id, user_id);

-- Keep the original detail table name for development schemas that created an
-- earlier, unreleased draft without recording a different migration checksum.
-- A recorded checksum mismatch must be repaired from the exact original SQL;
-- this migration intentionally does not guess or bypass unknown checksums.
CREATE TABLE IF NOT EXISTS user_group_account_allowlists (
    user_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    account_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, group_id, account_id),
    CONSTRAINT user_group_account_allowlists_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT user_group_account_allowlists_group_id_fkey
        FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
    CONSTRAINT user_group_account_allowlists_account_id_fkey
        FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT user_group_account_allowlists_scope_fkey
        FOREIGN KEY (user_id, group_id)
        REFERENCES user_group_account_allowlist_scopes(user_id, group_id)
        ON DELETE CASCADE
);

-- Upgrade an earlier draft safely: every existing detail set becomes an
-- explicit restricted scope before the composite foreign key is added.
INSERT INTO user_group_account_allowlist_scopes (user_id, group_id, created_at, updated_at)
SELECT user_id, group_id, MIN(created_at), MAX(updated_at)
FROM user_group_account_allowlists
GROUP BY user_id, group_id
ON CONFLICT (user_id, group_id) DO NOTHING;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'user_group_account_allowlists_scope_fkey'
          AND conrelid = 'user_group_account_allowlists'::regclass
    ) THEN
        ALTER TABLE user_group_account_allowlists
            ADD CONSTRAINT user_group_account_allowlists_scope_fkey
            FOREIGN KEY (user_id, group_id)
            REFERENCES user_group_account_allowlist_scopes(user_id, group_id)
            ON DELETE CASCADE
            NOT VALID;
        ALTER TABLE user_group_account_allowlists
            VALIDATE CONSTRAINT user_group_account_allowlists_scope_fkey;
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_user_group_account_allowlists_group_user
    ON user_group_account_allowlists (group_id, user_id);

CREATE INDEX IF NOT EXISTS idx_user_group_account_allowlists_account_id
    ON user_group_account_allowlists (account_id);

-- PostgreSQL notifications provide transaction-bound, cross-instance cache
-- invalidation without adding another runtime dependency. Process caches retain
-- a short TTL fallback so a disconnected listener cannot remain stale.
CREATE OR REPLACE FUNCTION notify_user_group_account_allowlist_change()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (OLD.user_id IS DISTINCT FROM NEW.user_id
            OR OLD.group_id IS DISTINCT FROM NEW.group_id) THEN
        PERFORM pg_notify(
            'xiass_user_group_account_allowlist',
            OLD.user_id::TEXT || ':' || OLD.group_id::TEXT
        );
        PERFORM pg_notify(
            'xiass_user_group_account_allowlist',
            NEW.user_id::TEXT || ':' || NEW.group_id::TEXT
        );
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        PERFORM pg_notify(
            'xiass_user_group_account_allowlist',
            OLD.user_id::TEXT || ':' || OLD.group_id::TEXT
        );
        RETURN OLD;
    END IF;

    PERFORM pg_notify(
        'xiass_user_group_account_allowlist',
        NEW.user_id::TEXT || ':' || NEW.group_id::TEXT
    );
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_user_group_account_allowlist_scopes_notify
    ON user_group_account_allowlist_scopes;
CREATE TRIGGER trg_user_group_account_allowlist_scopes_notify
AFTER INSERT OR UPDATE OR DELETE ON user_group_account_allowlist_scopes
FOR EACH ROW EXECUTE FUNCTION notify_user_group_account_allowlist_change();

DROP TRIGGER IF EXISTS trg_user_group_account_allowlists_notify
    ON user_group_account_allowlists;
CREATE TRIGGER trg_user_group_account_allowlists_notify
AFTER INSERT OR UPDATE OR DELETE ON user_group_account_allowlists
FOR EACH ROW EXECUTE FUNCTION notify_user_group_account_allowlist_change();

COMMENT ON TABLE user_group_account_allowlist_scopes IS
    'Restricted user/group account scopes. Presence means restricted even when no detail rows remain.';
COMMENT ON TABLE user_group_account_allowlists IS
    'Selected account details for restricted user/group scopes; an empty detail set denies all candidates.';
