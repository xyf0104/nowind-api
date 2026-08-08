-- Operator-defined queue order for XIASS SMS card keys. Claim counts remain
-- the primary rotation rule; queue_rank only orders cards with equal usage.
ALTER TABLE xiass_sms_card_keys
    ADD COLUMN IF NOT EXISTS queue_rank BIGINT NOT NULL DEFAULT 0;

UPDATE xiass_sms_card_keys
SET queue_rank = id
WHERE queue_rank = 0;

CREATE INDEX IF NOT EXISTS idx_xiass_sms_card_keys_queue_manual_order
    ON xiass_sms_card_keys (status, claim_count, queue_rank, last_claimed_at NULLS FIRST, id);
