-- Protect v15 quota state while older application nodes still run. No formula,
-- baseline, credential, or observation is reconstructed by this migration.
-- Keep this trigger and its revision marker on application rollback. Old nodes
-- may continue ordinary edits, but cannot write protected quota state.

CREATE OR REPLACE FUNCTION xiass_openai_weekly_integer(value JSONB)
RETURNS BIGINT LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE n NUMERIC;
BEGIN
    IF jsonb_typeof(value) IS DISTINCT FROM 'number' THEN RETURN NULL; END IF;
    n := value::TEXT::NUMERIC;
    IF n < 0 OR n > 9007199254740991 OR trunc(n) <> n THEN RETURN NULL; END IF;
    RETURN n::BIGINT;
END;
$$;

-- Retain nanosecond ordering rather than rounding RFC3339Nano to PG microseconds.
CREATE OR REPLACE FUNCTION xiass_openai_weekly_observation(value JSONB)
RETURNS NUMERIC LANGUAGE plpgsql STABLE AS $$
DECLARE parts TEXT[];
BEGIN
    IF jsonb_typeof(value) IS DISTINCT FROM 'string' THEN RETURN NULL; END IF;
    parts := regexp_match(value #>> '{}',
        '^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})(?:\.([0-9]{1,9}))?(Z|[+-][0-9]{2}:[0-9]{2})$');
    IF parts IS NULL THEN RETURN NULL; END IF;
    RETURN extract(epoch FROM (parts[1] || parts[3])::TIMESTAMPTZ)
        + COALESCE(('0.' || parts[2])::NUMERIC, 0);
EXCEPTION WHEN invalid_datetime_format OR datetime_field_overflow OR invalid_text_representation THEN
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION xiass_openai_weekly_managed_extra(value JSONB)
RETURNS JSONB LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(jsonb_object_agg(key, val), '{}'::JSONB)
    FROM jsonb_each(CASE WHEN jsonb_typeof(value) = 'object' THEN value ELSE '{}'::JSONB END) AS e(key, val)
    WHERE key = 'codex_usage_updated_at' OR key ~ '^codex_(primary_|secondary_|5h_|7d_|reset_credit_)';
$$;

CREATE OR REPLACE FUNCTION xiass_openai_weekly_raw_extra(value JSONB, credits BOOLEAN)
RETURNS JSONB LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(jsonb_object_agg(key, val), '{}'::JSONB)
    FROM jsonb_each(CASE WHEN jsonb_typeof(value) = 'object' THEN value ELSE '{}'::JSONB END) AS e(key, val)
    WHERE CASE WHEN credits THEN key ~ '^codex_reset_credit_'
        ELSE (key = 'codex_usage_updated_at' OR key ~ '^codex_(primary_|secondary_|5h_|7d_)')
            AND key !~ '^codex_7d_estimate_' END;
$$;

CREATE OR REPLACE FUNCTION xiass_openai_weekly_raw_write_allowed(old_extra JSONB, new_extra JSONB, credits BOOLEAN)
RETURNS BOOLEAN LANGUAGE plpgsql STABLE AS $$
DECLARE
    time_key TEXT := CASE WHEN credits THEN 'codex_reset_credit_snapshot_updated_at' ELSE 'codex_usage_updated_at' END;
    old_at NUMERIC;
    new_at NUMERIC;
BEGIN
    IF NOT COALESCE(xiass_openai_weekly_integer(old_extra #> '{codex_7d_estimate_baseline,version}') >= 15
        OR old_extra ? 'codex_7d_estimate_revision', FALSE) THEN RETURN TRUE; END IF;
    IF xiass_openai_weekly_raw_extra(old_extra, credits) = xiass_openai_weekly_raw_extra(new_extra, credits) THEN
        RETURN TRUE;
    END IF;
    old_at := xiass_openai_weekly_observation(old_extra -> time_key);
    new_at := xiass_openai_weekly_observation(new_extra -> time_key);
    RETURN new_at IS NOT NULL AND (old_at IS NULL OR new_at >= old_at);
END;
$$;

-- Used by both CAS's WHERE and the trigger: a DB-preserved write must never be
-- reported as a successful hypothetical estimate by the new application.
CREATE OR REPLACE FUNCTION xiass_openai_weekly_state_write_allowed(old_extra JSONB, new_extra JSONB, account_credentials JSONB)
RETURNS BOOLEAN LANGUAGE plpgsql STABLE AS $$
DECLARE
    old_version BIGINT := xiass_openai_weekly_integer(old_extra #> '{codex_7d_estimate_baseline,version}');
    new_version BIGINT := xiass_openai_weekly_integer(new_extra #> '{codex_7d_estimate_baseline,version}');
    old_revision BIGINT := xiass_openai_weekly_integer(old_extra -> 'codex_7d_estimate_revision');
    new_revision BIGINT := xiass_openai_weekly_integer(new_extra -> 'codex_7d_estimate_revision');
    old_at NUMERIC;
    new_at NUMERIC;
    fenced BOOLEAN := COALESCE(old_version >= 15 OR old_extra ? 'codex_7d_estimate_revision', FALSE);
BEGIN
    IF NOT fenced AND COALESCE(new_version, 0) < 15 THEN
        RETURN NOT COALESCE(new_extra ? 'codex_7d_estimate_revision', FALSE);
    END IF;
    IF (new_extra -> 'codex_7d_estimate_baseline') IS NOT DISTINCT FROM (old_extra -> 'codex_7d_estimate_baseline')
       AND (new_extra -> 'codex_7d_estimate_epoch') IS NOT DISTINCT FROM (old_extra -> 'codex_7d_estimate_epoch')
       AND (new_extra -> 'codex_7d_estimate_revision') IS NOT DISTINCT FROM (old_extra -> 'codex_7d_estimate_revision') THEN
        RETURN TRUE;
    END IF;
    IF NOT COALESCE(old_extra ? 'codex_7d_estimate_revision', FALSE) THEN old_revision := 0; END IF;
    IF old_revision IS NULL OR old_revision >= 9007199254740991
       OR new_revision IS NULL OR new_revision <> old_revision + 1 THEN RETURN FALSE; END IF;
    IF new_extra -> 'codex_7d_estimate_baseline' IS NULL
       OR new_extra -> 'codex_7d_estimate_baseline' = 'null'::JSONB THEN RETURN fenced; END IF;
    IF new_version IS NULL OR new_version < GREATEST(15, COALESCE(old_version, 0)) THEN RETURN FALSE; END IF;
    IF NULLIF(BTRIM(account_credentials ->> 'chatgpt_account_id'), '') IS NULL
       OR (new_extra #>> '{codex_7d_estimate_baseline,identity}') IS DISTINCT FROM (account_credentials ->> 'chatgpt_account_id') THEN
        RETURN FALSE;
    END IF;
    new_at := xiass_openai_weekly_observation(new_extra #> '{codex_7d_estimate_baseline,observed_at}');
    old_at := xiass_openai_weekly_observation(old_extra #> '{codex_7d_estimate_baseline,observed_at}');
    RETURN new_at IS NOT NULL AND (old_at IS NULL OR new_at >= old_at);
END;
$$;

-- Seed only the concurrency marker. Existing v15 payload/evidence is untouched.
UPDATE accounts SET extra = extra || '{"codex_7d_estimate_revision":1}'::JSONB
WHERE platform = 'openai' AND type = 'oauth'
  AND xiass_openai_weekly_integer(extra #> '{codex_7d_estimate_baseline,version}') >= 15
  AND NOT (extra ? 'codex_7d_estimate_revision');

CREATE OR REPLACE FUNCTION xiass_guard_openai_weekly_state()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    old_version BIGINT;
    old_revision BIGINT;
    protected BOOLEAN;
    same_identity BOOLEAN;
    clean JSONB;
    credits BOOLEAN;
    old_raw JSONB;
    new_raw JSONB;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.platform = 'openai' AND NEW.type = 'oauth'
           AND xiass_openai_weekly_integer(NEW.extra #> '{codex_7d_estimate_baseline,version}') >= 15 THEN
            NEW.extra := NEW.extra || '{"codex_7d_estimate_revision":1}'::JSONB;
        END IF;
        RETURN NEW;
    END IF;
    old_version := xiass_openai_weekly_integer(OLD.extra #> '{codex_7d_estimate_baseline,version}');
    protected := COALESCE(OLD.extra ? 'codex_7d_estimate_revision', FALSE)
        OR (OLD.platform = 'openai' AND OLD.type = 'oauth' AND COALESCE(old_version, 0) >= 15);
    IF NOT protected AND NOT (NEW.platform = 'openai' AND NEW.type = 'oauth'
        AND COALESCE(xiass_openai_weekly_integer(NEW.extra #> '{codex_7d_estimate_baseline,version}'), 0) >= 15) THEN
        RETURN NEW;
    END IF;
    clean := CASE WHEN jsonb_typeof(NEW.extra) = 'object' THEN NEW.extra ELSE '{}'::JSONB END;
    -- Token rotation preserves history only with unchanged principal fields.
    -- Missing-to-known user identity is not proof that old history belongs to it.
    same_identity := OLD.platform = NEW.platform AND OLD.type = NEW.type AND (
        OLD.credentials IS NOT DISTINCT FROM NEW.credentials OR (
            NULLIF(BTRIM(OLD.credentials ->> 'chatgpt_account_id'), '') IS NOT NULL
            AND OLD.credentials ->> 'chatgpt_account_id' = NEW.credentials ->> 'chatgpt_account_id'
            AND (OLD.credentials ->> 'chatgpt_user_id') IS NOT DISTINCT FROM (NEW.credentials ->> 'chatgpt_user_id')
        ));
    IF protected AND NOT COALESCE(same_identity, FALSE) THEN
        old_revision := xiass_openai_weekly_integer(OLD.extra -> 'codex_7d_estimate_revision');
        IF NOT COALESCE(OLD.extra ? 'codex_7d_estimate_revision', FALSE) THEN old_revision := 0; END IF;
        IF old_revision IS NULL THEN
            RAISE EXCEPTION 'Invalid OpenAI weekly state revision' USING ERRCODE = '22023';
        END IF;
        IF old_revision >= 9007199254740991 THEN
            RAISE EXCEPTION 'OpenAI weekly state revision exhausted' USING ERRCODE = '22003';
        END IF;
        -- Retain the fence across identity changes/clears to prevent ABA replay.
        NEW.extra := (clean - ARRAY(SELECT jsonb_object_keys(xiass_openai_weekly_managed_extra(clean))))
            || jsonb_build_object('codex_7d_estimate_revision', old_revision + 1);
        RETURN NEW;
    END IF;
    IF NOT xiass_openai_weekly_state_write_allowed(OLD.extra, NEW.extra, NEW.credentials) THEN
        -- Legacy whole-form writes may still update ordinary account settings.
        NEW.extra := (clean - ARRAY(SELECT jsonb_object_keys(xiass_openai_weekly_managed_extra(clean))))
            || xiass_openai_weekly_managed_extra(OLD.extra);
        RETURN NEW;
    END IF;
    -- A full form can carry the current estimator but an older raw snapshot.
    -- Usage and reset credits have independent clocks. A credential/proxy edit
    -- cannot authenticate bundled quota; capture must be applied by bound CAS.
    FOREACH credits IN ARRAY ARRAY[FALSE, TRUE] LOOP
        old_raw := xiass_openai_weekly_raw_extra(OLD.extra, credits);
        new_raw := xiass_openai_weekly_raw_extra(clean, credits);
        IF old_raw IS DISTINCT FROM new_raw AND (
            OLD.credentials IS DISTINCT FROM NEW.credentials OR OLD.proxy_id IS DISTINCT FROM NEW.proxy_id
            OR NOT xiass_openai_weekly_raw_write_allowed(OLD.extra, clean, credits)
        ) THEN
            clean := (clean - ARRAY(SELECT jsonb_object_keys(new_raw))) || old_raw;
        END IF;
    END LOOP;
    NEW.extra := clean;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS accounts_guard_openai_weekly_state ON accounts;
CREATE TRIGGER accounts_guard_openai_weekly_state
BEFORE INSERT OR UPDATE OF extra, credentials, platform, type ON accounts
FOR EACH ROW EXECUTE FUNCTION xiass_guard_openai_weekly_state();
