-- Allow administrators to hide selected public groups from individual users.
-- Existing users keep the historical behavior because the restriction is off
-- by default. When enabled, user_allowed_groups becomes the explicit public
-- group allowlist while retaining its existing exclusive-group grant role.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS restrict_public_groups BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN users.restrict_public_groups IS
    'When true, non-exclusive standard groups must also exist in user_allowed_groups.';

-- Keep the existing multi-instance auth-cache outbox contract aware of the new
-- authorization field. The trigger itself already points at this function.
CREATE OR REPLACE FUNCTION enqueue_user_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_user_id BIGINT;
BEGIN
    target_user_id := OLD.id;
    IF TG_OP = 'UPDATE'
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.role IS NOT DISTINCT FROM NEW.role
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at
       AND OLD.restrict_public_groups IS NOT DISTINCT FROM NEW.restrict_public_groups THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
    FROM api_keys AS k
    WHERE k.user_id = target_user_id
      AND k.deleted_at IS NULL
      AND k.key <> '';
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

-- Migration 184 only invalidated user_allowed_groups changes for exclusive
-- groups. Once a user restricts public groups, the same join table is also the
-- public-group allowlist, so its changes must invalidate matching API keys on
-- every instance through the durable outbox.
CREATE OR REPLACE FUNCTION enqueue_allowed_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_user_id BIGINT;
    target_group_id BIGINT;
BEGIN
    IF TG_OP = 'UPDATE'
       AND (OLD.user_id IS DISTINCT FROM NEW.user_id
            OR OLD.group_id IS DISTINCT FROM NEW.group_id) THEN
        IF EXISTS (
            SELECT 1
            FROM groups g
            WHERE g.id = OLD.group_id
              AND (
                  g.is_exclusive = TRUE
                  OR EXISTS (
                      SELECT 1
                      FROM users u
                      WHERE u.id = OLD.user_id
                        AND u.restrict_public_groups = TRUE
                  )
              )
        ) THEN
            INSERT INTO auth_cache_invalidation_outbox (cache_key)
            SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
            FROM api_keys AS k
            WHERE k.user_id = OLD.user_id
              AND k.group_id = OLD.group_id
              AND k.deleted_at IS NULL
              AND k.key <> '';
        END IF;
        target_user_id := NEW.user_id;
        target_group_id := NEW.group_id;
    ELSIF TG_OP = 'UPDATE' THEN
        RETURN NEW;
    ELSIF TG_OP = 'INSERT' THEN
        target_user_id := NEW.user_id;
        target_group_id := NEW.group_id;
    ELSE
        target_user_id := OLD.user_id;
        target_group_id := OLD.group_id;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM groups g
        WHERE g.id = target_group_id
          AND (
              g.is_exclusive = TRUE
              OR EXISTS (
                  SELECT 1
                  FROM users u
                  WHERE u.id = target_user_id
                    AND u.restrict_public_groups = TRUE
              )
          )
    ) THEN
        INSERT INTO auth_cache_invalidation_outbox (cache_key)
        SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
        FROM api_keys AS k
        WHERE k.user_id = target_user_id
          AND k.group_id = target_group_id
          AND k.deleted_at IS NULL
          AND k.key <> '';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;
