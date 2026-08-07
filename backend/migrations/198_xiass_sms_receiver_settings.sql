-- Runtime controls for the public XIASS SMS receiver. The single row keeps
-- administrator-owned pricing independent from general system settings while
-- allowing each member claim to snapshot its exact fee in the charge ledger.
CREATE TABLE IF NOT EXISTS xiass_sms_receiver_settings (
    id          SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    member_fee  NUMERIC(12, 2) NOT NULL DEFAULT 2.00 CHECK (member_fee > 0 AND member_fee <= 10000),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO xiass_sms_receiver_settings (id, member_fee)
VALUES (1, 2.00)
ON CONFLICT (id) DO NOTHING;
