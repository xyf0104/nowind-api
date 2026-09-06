//go:build integration

package repository

import (
	"context"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration237IsSafeNoOpForMalformedAndUntrustedAuditEvidence(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	migrationSQL, err := dbmigrations.FS.ReadFile("237_backfill_openai_weekly_estimate_join_baselines.sql")
	require.NoError(t, err)

	var accountIDs []int64
	insertAccount := func(name, extra string) int64 {
		var id int64
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, credentials, extra)
VALUES ($1, 'openai', 'oauth', '{"chatgpt_account_id":"migration-237"}'::jsonb, $2::jsonb)
RETURNING id
`, name, extra).Scan(&id))
		accountIDs = append(accountIDs, id)
		return id
	}

	malformedID := insertAccount("migration-237-malformed", `{"codex_7d_estimate_baseline":{"version":14,"mode":"explicit","snapshot_cost":12}}`)
	missingCostID := insertAccount("migration-237-missing-cost", `{"codex_7d_estimate_baseline":{"version":14,"mode":"join_average"}}`)
	previousWeekID := insertAccount("migration-237-previous-week", `{"codex_7d_estimate_baseline":{"version":14,"mode":"join_average","baseline_percent":20,"baseline_cost":100,"percent_bucket":25,"snapshot_cost":120,"reset_at":"2020-01-08T00:00:00Z","identity":"migration-237"}}`)
	explicitModeID := insertAccount("migration-237-explicit-mode", `{"codex_7d_estimate_baseline":{"version":14,"mode":"explicit","baseline_percent":20,"baseline_cost":100,"percent_bucket":25,"snapshot_cost":120,"reset_at":"2099-01-08T00:00:00Z","identity":"migration-237"}}`)

	insertAudit := func(body string) {
		_, err := tx.ExecContext(ctx, `
INSERT INTO audit_logs (action, method, path, request_body)
VALUES ('admin.accounts.update', 'PUT', '/api/v1/admin/accounts/1', $1)
`, body)
		require.NoError(t, err)
	}
	insertAudit(`{"credentials":{"chatgpt_account_id":"migration-237"},"extra":{"codex_7d_used_percent":25,"codex_7d_estimate_baseline":{"snapshot_cost":120}}}`)
	insertAudit(`{"credentials":{"chatgpt_account_id":"migration-237"},"extra":{"codex_7d_used_percent":26}}`)
	insertAudit(`{"credentials":{"chatgpt_account_id":"migration-237"},"extra":{"codex_7d_used_percent":25,"codex_7d_estimate_baseline":{"snapshot_cost":120}}`)
	insertAudit("<body omitted: exceeds 262144 bytes>")

	before := map[int64]string{}
	for _, id := range accountIDs {
		var extra string
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT extra::text FROM accounts WHERE id = $1`, id).Scan(&extra))
		before[id] = extra
	}

	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)

	for _, id := range accountIDs {
		var after string
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT extra::text FROM accounts WHERE id = $1`, id).Scan(&after))
		require.Equal(t, before[id], after, "migration 237 must not mutate account %d", id)
	}
	// Keep variables tied to the assertions so the fixtures remain explicit.
	require.NotZero(t, malformedID)
	require.NotZero(t, missingCostID)
	require.NotZero(t, previousWeekID)
	require.NotZero(t, explicitModeID)
}
