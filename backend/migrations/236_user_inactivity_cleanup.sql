-- Durable state for the inactive-user warning and deletion workflow.
-- The row is a single active cleanup cycle per user. It is removed when the
-- user becomes active again or after the user has been soft-deleted.
CREATE TABLE IF NOT EXISTS user_inactivity_cleanups (
    user_id              BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    activity_at          TIMESTAMPTZ NOT NULL,
    reminder_status      VARCHAR(20) NOT NULL DEFAULT 'pending',
    reminder_claimed_at  TIMESTAMPTZ,
    reminder_sent_at     TIMESTAMPTZ,
    delete_after         TIMESTAMPTZ NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT user_inactivity_cleanups_status_check
        CHECK (reminder_status IN ('pending', 'sending', 'sent'))
);

CREATE INDEX IF NOT EXISTS idx_user_inactivity_cleanups_due
    ON user_inactivity_cleanups (delete_after)
    WHERE reminder_sent_at IS NOT NULL;
