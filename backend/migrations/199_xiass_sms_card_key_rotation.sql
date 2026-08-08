-- Card keys may be retried for a limited number of authorization attempts.
-- Track claims independently from terminal provider state so released keys are
-- selected by lowest claim count, then least-recently-used order, instead of
-- repeatedly returning to the front of the queue.
ALTER TABLE xiass_sms_card_keys
    ADD COLUMN IF NOT EXISTS claim_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_claimed_at TIMESTAMPTZ NULL;

ALTER TABLE xiass_sms_card_keys
    DROP CONSTRAINT IF EXISTS xiass_sms_card_keys_status_check;

ALTER TABLE xiass_sms_card_keys
    ADD CONSTRAINT xiass_sms_card_keys_status_check
    CHECK (status IN ('queued', 'active', 'completed', 'cancelled', 'expired', 'failed', 'exhausted'));

CREATE INDEX IF NOT EXISTS idx_xiass_sms_card_keys_rotation
    ON xiass_sms_card_keys (status, claim_count, last_claimed_at NULLS FIRST, id);
