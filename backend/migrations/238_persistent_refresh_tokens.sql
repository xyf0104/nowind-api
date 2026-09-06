-- PostgreSQL is the sole authority for refresh credentials in this opt-in store.
-- No Redis backfill, business-data seed, or runtime cutover is performed here.
-- Tombstones and revocation fences MUST NOT be TTL-deleted: an arbitrarily late
-- writer must not be able to recreate a consumed hash or a revoked session.
-- Deliberately no cascading FK to users: deleting a user must retain its fence.
CREATE TABLE IF NOT EXISTS refresh_token_revocation_state (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    generation BIGINT NOT NULL DEFAULT 0 CHECK (generation >= 0),
    revoked_at TIMESTAMPTZ
);
INSERT INTO refresh_token_revocation_state (singleton) VALUES (TRUE)
    ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS refresh_token_users (
    user_id BIGINT PRIMARY KEY CHECK (user_id > 0),
    generation BIGINT NOT NULL DEFAULT 0 CHECK (generation >= 0),
    revoked_at TIMESTAMPTZ
);

-- Preparation commits before credential generation. Tickets are single-use and
-- never reconstructed from caller timestamps or Redis. Their generations are
-- checked against the authoritative locked scope rows at Store.
CREATE TABLE IF NOT EXISTS refresh_token_issuances (
    ticket_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL REFERENCES refresh_token_users(user_id),
    user_generation BIGINT NOT NULL CHECK (user_generation >= 0),
    global_generation BIGINT NOT NULL CHECK (global_generation >= 0),
    prepared_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (clock_timestamp() + INTERVAL '5 minutes'),
    used_at TIMESTAMPTZ,
    CHECK (expires_at > prepared_at)
);
CREATE INDEX IF NOT EXISTS idx_refresh_token_issuances_user ON refresh_token_issuances(user_id);

CREATE TABLE IF NOT EXISTS refresh_token_families (
    family_id TEXT PRIMARY KEY CHECK (family_id ~ '^[0-9a-f]{32}$'),
    user_id BIGINT REFERENCES refresh_token_users(user_id),
    family_expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    UNIQUE (family_id, user_id),
    CHECK ((user_id IS NOT NULL AND family_expires_at IS NOT NULL) OR
           (user_id IS NULL AND family_expires_at IS NULL AND revoked_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS idx_refresh_token_families_user
    ON refresh_token_families(user_id);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    token_hash TEXT PRIMARY KEY CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    user_id BIGINT,
    family_id TEXT,
    token_version BIGINT,
    binding_hash TEXT CHECK (binding_hash = '' OR binding_hash ~ '^([0-9a-f]{32}|[0-9a-f]{64})$'),
    created_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    valid_until TIMESTAMPTZ,
    issuance_id UUID UNIQUE REFERENCES refresh_token_issuances(ticket_id),
    user_generation BIGINT,
    global_generation BIGINT,
    consumed_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    FOREIGN KEY (family_id, user_id) REFERENCES refresh_token_families(family_id, user_id),
    CHECK ((user_id IS NOT NULL AND family_id IS NOT NULL AND token_version IS NOT NULL AND
            binding_hash IS NOT NULL AND created_at IS NOT NULL AND expires_at IS NOT NULL AND
            valid_until IS NOT NULL AND issuance_id IS NOT NULL AND
            user_generation IS NOT NULL AND user_generation >= 0 AND
            global_generation IS NOT NULL AND global_generation >= 0 AND expires_at > created_at AND
            valid_until > created_at AND valid_until <= expires_at) OR
           (user_id IS NULL AND family_id IS NULL AND token_version IS NULL AND
            binding_hash IS NULL AND created_at IS NULL AND expires_at IS NULL AND
            valid_until IS NULL AND issuance_id IS NULL AND user_generation IS NULL AND
            global_generation IS NULL AND consumed_at IS NULL AND revoked_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user ON refresh_tokens(user_id, token_hash);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_family ON refresh_tokens(family_id, token_hash);
