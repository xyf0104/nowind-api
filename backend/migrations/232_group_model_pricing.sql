-- XIASS group-level model pricing. This is additive and intentionally keeps
-- the established static/long-context pricing contract; no time-pricing data
-- or columns are introduced here.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS long_context_pricing_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS model_pricing JSONB NOT NULL DEFAULT '[]'::jsonb;

-- A partially applied historical upstream schema may have left nullable
-- columns. Normalize it before strengthening the long-term defaults.
UPDATE groups
SET long_context_pricing_enabled = TRUE
WHERE long_context_pricing_enabled IS DISTINCT FROM TRUE;

UPDATE groups
SET model_pricing = '[]'::jsonb
WHERE model_pricing IS NULL
   OR model_pricing = 'null'::jsonb;

ALTER TABLE groups
    ALTER COLUMN long_context_pricing_enabled SET DEFAULT TRUE,
    ALTER COLUMN long_context_pricing_enabled SET NOT NULL,
    ALTER COLUMN model_pricing SET DEFAULT '[]'::jsonb,
    ALTER COLUMN model_pricing SET NOT NULL;

COMMENT ON COLUMN groups.long_context_pricing_enabled IS
    'Whether group model pricing selects long-context intervals; default true preserves XIASS billing behavior';
COMMENT ON COLUMN groups.model_pricing IS
    'Per-model group pricing overrides channel and built-in pricing; static pricing only, no time pricing';
