-- Member-facing XIASS SMS billing. A session holds ¥2.00 when a number is
-- claimed, captures it only after a real code arrives, and releases it for
-- cancellation, timeout, and other non-code terminal paths.
CREATE TABLE IF NOT EXISTS xiass_sms_member_charges (
    session_id   VARCHAR(64) PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    amount       NUMERIC(12, 2) NOT NULL CHECK (amount > 0),
    status       VARCHAR(16) NOT NULL DEFAULT 'held',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    captured_at  TIMESTAMPTZ NULL,
    released_at  TIMESTAMPTZ NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiass_sms_member_charges_status_check
        CHECK (status IN ('held', 'captured', 'released'))
);

CREATE INDEX IF NOT EXISTS idx_xiass_sms_member_charges_user_created
    ON xiass_sms_member_charges (user_id, created_at DESC);

-- A member may have one live phone number. It also gives the database a final
-- integrity guard if two browser tabs race beyond the advisory lock.
CREATE UNIQUE INDEX IF NOT EXISTS uq_xiass_sms_card_keys_active_owner
    ON xiass_sms_card_keys (owner_user_id)
    WHERE status = 'active' AND owner_user_id IS NOT NULL;
