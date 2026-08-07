-- Server-side Pixlab SMS card-key queue.
-- Raw card keys are AES-GCM encrypted by the application before storage; the
-- SHA-256 fingerprint is used solely for de-duplication and is never exposed.
CREATE TABLE IF NOT EXISTS xiass_sms_card_keys (
    id              BIGSERIAL PRIMARY KEY,
    encrypted_key   TEXT NOT NULL,
    key_fingerprint CHAR(64) NOT NULL UNIQUE,
    status          VARCHAR(16) NOT NULL DEFAULT 'queued',
    owner_user_id   BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    session_id      VARCHAR(64) NULL UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    consumed_at     TIMESTAMPTZ NULL,
    completed_at    TIMESTAMPTZ NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiass_sms_card_keys_status_check
        CHECK (status IN ('queued', 'active', 'completed', 'cancelled', 'expired', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_xiass_sms_card_keys_queue
    ON xiass_sms_card_keys (id)
    WHERE status = 'queued';

CREATE INDEX IF NOT EXISTS idx_xiass_sms_card_keys_active_owner
    ON xiass_sms_card_keys (owner_user_id, session_id)
    WHERE status = 'active';
