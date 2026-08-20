package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupModelPricingMigrationPreservesLongContextDefaults(t *testing.T) {
	content, err := FS.ReadFile("232_group_model_pricing.sql")
	require.NoError(t, err)

	sql := strings.ToLower(string(content))
	require.Contains(t, sql, "long_context_pricing_enabled boolean not null default true")
	require.Contains(t, sql, "model_pricing jsonb not null default '[]'::jsonb")
	require.Contains(t, sql, "alter column long_context_pricing_enabled set default true")
	require.Contains(t, sql, "alter column model_pricing set default '[]'::jsonb")
	require.Contains(t, sql, "where model_pricing is null")
	require.Contains(t, sql, "or model_pricing = 'null'::jsonb")
	require.NotContains(t, sql, "time_pricing")
}
