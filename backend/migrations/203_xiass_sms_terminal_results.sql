-- Recoverable Pixlab terminal results for sessions finalized after the browser
-- has closed. Payloads are encrypted by the application before storage.
CREATE TABLE IF NOT EXISTS xiass_sms_terminal_results (
    session_id        VARCHAR(64) PRIMARY KEY,
    owner_user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    member_session    BOOLEAN NOT NULL,
    encrypted_payload TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at        TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
);

CREATE INDEX IF NOT EXISTS idx_xiass_sms_terminal_results_owner
    ON xiass_sms_terminal_results (owner_user_id, member_session, created_at DESC);

-- Rotate transiently failing cleanup candidates behind sessions that have not
-- yet received a final-check attempt, preventing a fixed oldest-row backlog.
ALTER TABLE xiass_sms_card_keys
    ADD COLUMN IF NOT EXISTS cleanup_attempted_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS cleanup_lease_token VARCHAR(64) NULL,
    ADD COLUMN IF NOT EXISTS cleanup_lease_until TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_xiass_sms_card_keys_cleanup_rotation
    ON xiass_sms_card_keys (cleanup_attempted_at NULLS FIRST, consumed_at, id)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_xiass_sms_card_keys_cleanup_lease
    ON xiass_sms_card_keys (cleanup_lease_until)
    WHERE status = 'active' AND cleanup_lease_until IS NOT NULL;
