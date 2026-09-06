-- No automatic adoption or authority switch. Preserve 238/240 and dedicated
-- transition witnesses; a NULL manifest denotes the original dedicated mode.
ALTER TABLE refresh_token_legacy_transition ADD COLUMN group_manifest JSONB;

CREATE TABLE refresh_token_transition_nodes (
    transition_id UUID NOT NULL REFERENCES refresh_token_legacy_transition(transition_id),
    run_id TEXT NOT NULL CHECK (run_id ~ '^[0-9a-f]{40}$'),
    acl_sha256 TEXT NOT NULL CHECK (acl_sha256 ~ '^[0-9a-f]{64}$'),
    fenced_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (transition_id, run_id)
);

CREATE FUNCTION retain_refresh_token_group_inventory() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.group_manifest IS DISTINCT FROM OLD.group_manifest THEN
        RAISE EXCEPTION 'refresh token transition inventory is immutable';
    END IF;
    IF NEW.group_manifest IS NOT NULL AND NEW.state IN ('fenced','completed') AND (
        jsonb_typeof(NEW.group_manifest->'Nodes') IS DISTINCT FROM 'array' OR
        jsonb_array_length(NEW.group_manifest->'Nodes') NOT BETWEEN 1 AND 9 OR
        EXISTS (
            SELECT 1 FROM jsonb_array_elements(NEW.group_manifest->'Nodes') n
            WHERE NOT EXISTS (SELECT 1 FROM refresh_token_transition_nodes p
                WHERE p.transition_id = NEW.transition_id AND p.run_id = n->>'RunID')
        )
    ) THEN
        RAISE EXCEPTION 'refresh token group requires every durable node fence';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER refresh_token_transition_inventory_guard
    BEFORE UPDATE ON refresh_token_legacy_transition
    FOR EACH ROW EXECUTE FUNCTION retain_refresh_token_group_inventory();

CREATE FUNCTION validate_refresh_token_node_fence() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM refresh_token_legacy_transition t
        WHERE t.transition_id = NEW.transition_id AND t.state = 'preparing'
          AND EXISTS (SELECT 1 FROM jsonb_array_elements(t.group_manifest->'Nodes') n
                      WHERE n->>'RunID' = NEW.run_id)
    ) THEN
        RAISE EXCEPTION 'node fence must belong to the preparing group inventory';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER refresh_token_node_insert_guard
    BEFORE INSERT ON refresh_token_transition_nodes
    FOR EACH ROW EXECUTE FUNCTION validate_refresh_token_node_fence();

CREATE FUNCTION retain_refresh_token_node_fence() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'refresh token node fence witness is append-only';
END;
$$;
CREATE TRIGGER refresh_token_node_fence_guard
    BEFORE UPDATE OR DELETE ON refresh_token_transition_nodes
    FOR EACH ROW EXECUTE FUNCTION retain_refresh_token_node_fence();
CREATE TRIGGER refresh_token_node_fence_truncate_guard
    BEFORE TRUNCATE ON refresh_token_transition_nodes
    FOR EACH STATEMENT EXECUTE FUNCTION retain_refresh_token_node_fence();
