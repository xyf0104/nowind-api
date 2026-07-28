package migrations

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstream166MigrationsAppendAfterReleasedXIASSMigrations(t *testing.T) {
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)

	positions := make(map[string]int, len(entries))
	for i, entry := range entries {
		positions[entry.Name()] = i
	}

	ordered := []string{
		"184_auth_cache_invalidation_outbox.sql",
		"185_composite_model_routes.sql",
		"186_group_reasoning_effort_policy.sql",
		"187_alipay_mobile_precreate_deep_link.sql",
		"188_group_auth_cache_image_generation.sql",
		"189_add_usage_log_session_id.sql",
		"190_allow_live_usage_request_type.sql",
		"191_add_group_allow_live.sql",
		"192_add_users_email_alias_dedup_index_notx.sql",
	}
	for i, name := range ordered {
		require.Contains(t, positions, name)
		if i > 0 {
			require.Less(t, positions[ordered[i-1]], positions[name], fmt.Sprintf("%s must follow %s", name, ordered[i-1]))
		}
	}

	legacyUpstreamNames := []string{
		"172_composite_model_routes.sql",
		"185_group_reasoning_effort_policy.sql",
		"186_alipay_mobile_precreate_deep_link.sql",
		"186_group_auth_cache_image_generation.sql",
		"187_add_usage_log_session_id.sql",
		"188_allow_live_usage_request_type.sql",
		"189_add_group_allow_live.sql",
		"190_add_users_email_alias_dedup_index_notx.sql",
	}
	for _, name := range legacyUpstreamNames {
		require.NotContains(t, positions, name)
	}
}
