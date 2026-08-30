-- One durable delivery record per (announcement, user). The unique key is the
-- source of truth for manual announcement-email deduplication across clicks,
-- application instances, and restarts.
CREATE TABLE IF NOT EXISTS announcement_email_deliveries (
    id               BIGSERIAL PRIMARY KEY,
    announcement_id  BIGINT NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    user_id          BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_email  VARCHAR(320) NOT NULL,
    status           VARCHAR(20) NOT NULL DEFAULT 'claimed',
    attempted_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at          TIMESTAMPTZ,
    error_message    TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT announcement_email_deliveries_announcement_user_unique UNIQUE (announcement_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_announcement_email_deliveries_announcement_status
    ON announcement_email_deliveries (announcement_id, status);
