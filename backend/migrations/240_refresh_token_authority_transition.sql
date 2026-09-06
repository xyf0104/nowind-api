-- Explicit operator-driven transition only. Applying this migration does not
-- switch any installation, copy Redis data, or expire existing sessions.
CREATE TABLE refresh_token_authority (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    backend TEXT NOT NULL DEFAULT 'redis' CHECK (backend IN ('redis', 'postgres')),
    activated_at TIMESTAMPTZ,
    CHECK ((backend = 'redis' AND activated_at IS NULL) OR
           (backend = 'postgres' AND activated_at IS NOT NULL))
);
INSERT INTO refresh_token_authority (singleton) VALUES (TRUE);

-- Permanent transition witness. No credentials, Redis bodies or raw tokens.
CREATE TABLE refresh_token_legacy_transition (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    transition_id UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    source_run_id TEXT NOT NULL,
    source_repl_id TEXT NOT NULL,
    source_address TEXT NOT NULL,
    source_db INTEGER NOT NULL CHECK (source_db >= 0),
    fence_password_sha256 TEXT NOT NULL CHECK (fence_password_sha256 ~ '^[0-9a-f]{64}$'),
    state TEXT NOT NULL DEFAULT 'preparing' CHECK (state IN ('preparing', 'fenced', 'completed')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    fenced_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    acl_sha256 TEXT,
    snapshot_sha256 TEXT,
    imported_count BIGINT NOT NULL DEFAULT 0 CHECK (imported_count >= 0),
    expired_count BIGINT NOT NULL DEFAULT 0 CHECK (expired_count >= 0),
    CHECK (state = 'preparing' OR (fenced_at IS NOT NULL AND acl_sha256 IS NOT NULL)),
    CHECK (state <> 'completed' OR (completed_at IS NOT NULL AND snapshot_sha256 IS NOT NULL))
);

CREATE FUNCTION enforce_refresh_token_authority_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP IN ('DELETE', 'TRUNCATE') THEN
        RAISE EXCEPTION 'refresh token authority marker cannot be removed';
    END IF;
    IF OLD.backend = 'postgres' AND
       (NEW.backend IS DISTINCT FROM OLD.backend OR NEW.activated_at IS DISTINCT FROM OLD.activated_at) THEN
        RAISE EXCEPTION 'refresh token authority activation is irreversible';
    END IF;
    IF OLD.backend = 'redis' AND NEW.backend = 'postgres' AND NOT EXISTS (
        SELECT 1 FROM refresh_token_legacy_transition
        WHERE singleton = TRUE AND state = 'completed'
          AND completed_at = NEW.activated_at AND fenced_at IS NOT NULL
          AND acl_sha256 ~ '^[0-9a-f]{64}$' AND snapshot_sha256 ~ '^[0-9a-f]{64}$'
    ) THEN
        RAISE EXCEPTION 'refresh token activation requires completed transition witness';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER refresh_token_authority_update_guard
    BEFORE UPDATE OR DELETE ON refresh_token_authority
    FOR EACH ROW EXECUTE FUNCTION enforce_refresh_token_authority_transition();
CREATE TRIGGER refresh_token_authority_truncate_guard
    BEFORE TRUNCATE ON refresh_token_authority
    FOR EACH STATEMENT EXECUTE FUNCTION enforce_refresh_token_authority_transition();

CREATE FUNCTION retain_refresh_token_transition_witness() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP IN ('DELETE', 'TRUNCATE') THEN
        RAISE EXCEPTION 'refresh token transition witness cannot be removed';
    END IF;
    IF OLD.state = 'completed' AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'completed refresh token transition witness is immutable';
    END IF;
    IF OLD.state = 'fenced' AND NEW.state = 'preparing' THEN
        RAISE EXCEPTION 'refresh token fence witness cannot move backwards';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER refresh_token_transition_witness_update_guard
    BEFORE UPDATE OR DELETE ON refresh_token_legacy_transition
    FOR EACH ROW EXECUTE FUNCTION retain_refresh_token_transition_witness();
CREATE TRIGGER refresh_token_transition_witness_truncate_guard
    BEFORE TRUNCATE ON refresh_token_legacy_transition
    FOR EACH STATEMENT EXECUTE FUNCTION retain_refresh_token_transition_witness();
