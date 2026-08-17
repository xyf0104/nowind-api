//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

var freshInstallDatabaseSequence uint64

func TestFreshInstallBusinessSeedPolicy_Postgres(t *testing.T) {
	t.Run("empty database records seeds without inserting defaults", func(t *testing.T) {
		db := newIsolatedMigrationDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		require.NoError(t, ApplyMigrations(ctx, db))

		for _, table := range []string{
			"groups",
			"channels",
			"channel_groups",
			"channel_model_pricing",
			"channel_pricing_intervals",
			"channel_account_stats_pricing_rules",
			"channel_account_stats_model_pricing",
			"channel_account_stats_pricing_intervals",
			"user_group_rate_multipliers",
		} {
			requireTableRowCount(t, db, table, 0)
		}

		for _, filename := range []string{
			"008_seed_default_group.sql",
			"157_seed_default_channel_pricing.sql",
			"173_seed_nowind_v1061_models_pricing.sql",
		} {
			var checksum string
			err := db.QueryRowContext(ctx,
				"SELECT checksum FROM schema_migrations WHERE filename = $1",
				filename,
			).Scan(&checksum)
			require.NoError(t, err, "missing schema_migrations row for %s", filename)
			require.Equal(t, embeddedMigrationChecksum(t, filename), checksum, filename)
		}
	})

	t.Run("existing business table executes seeds", func(t *testing.T) {
		db := newIsolatedMigrationDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		_, err := db.ExecContext(ctx, `
			CREATE TABLE settings (
				id BIGSERIAL PRIMARY KEY,
				key VARCHAR(100) NOT NULL UNIQUE,
				value TEXT NOT NULL,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
			INSERT INTO settings (key, value) VALUES ('existing_install_marker', 'preserve-me');
		`)
		require.NoError(t, err)

		require.NoError(t, ApplyMigrations(ctx, db))
		requireSeedMigrationsExecuted(t, db)

		var value string
		require.NoError(t, db.QueryRowContext(ctx,
			"SELECT value FROM settings WHERE key = 'existing_install_marker'",
		).Scan(&value))
		require.Equal(t, "preserve-me", value)
	})

	t.Run("existing migration history executes seeds", func(t *testing.T) {
		db := newIsolatedMigrationDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		_, err := db.ExecContext(ctx, schemaMigrationsTableDDL)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx,
			"INSERT INTO schema_migrations (filename, checksum) VALUES ($1, $2)",
			"legacy_install_marker.sql", "legacy-checksum",
		)
		require.NoError(t, err)

		require.NoError(t, ApplyMigrations(ctx, db))
		requireSeedMigrationsExecuted(t, db)

		var checksum string
		require.NoError(t, db.QueryRowContext(ctx,
			"SELECT checksum FROM schema_migrations WHERE filename = 'legacy_install_marker.sql'",
		).Scan(&checksum))
		require.Equal(t, "legacy-checksum", checksum)
	})
}

func newIsolatedMigrationDatabase(t *testing.T) *sql.DB {
	t.Helper()

	databaseName := fmt.Sprintf("xiass_fresh_seed_%d", atomic.AddUint64(&freshInstallDatabaseSequence, 1))
	quotedDatabaseName := pq.QuoteIdentifier(databaseName)
	_, err := integrationDB.Exec("CREATE DATABASE " + quotedDatabaseName)
	require.NoError(t, err)

	dsn, err := url.Parse(integrationPostgresDSN)
	require.NoError(t, err)
	dsn.Path = "/" + databaseName

	db, err := openSQLWithRetry(context.Background(), dsn.String(), 30*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		_, dropErr := integrationDB.Exec("DROP DATABASE " + quotedDatabaseName + " WITH (FORCE)")
		require.NoError(t, dropErr)
	})
	return db
}

func requireTableRowCount(t *testing.T, db *sql.DB, table string, expected int) {
	t.Helper()

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM " + pq.QuoteIdentifier(table)).Scan(&count)
	require.NoError(t, err, table)
	require.Equal(t, expected, count, table)
}

func embeddedMigrationChecksum(t *testing.T, filename string) string {
	t.Helper()

	content, err := migrations.FS.ReadFile(filename)
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(content))))
	return hex.EncodeToString(sum[:])
}

func requireSeedMigrationsExecuted(t *testing.T, db *sql.DB) {
	t.Helper()

	var defaultGroups int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM groups WHERE name = 'default'",
	).Scan(&defaultGroups))
	require.Equal(t, 1, defaultGroups, "008 seed should execute for an existing installation")

	var seededChannels int
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*)
		FROM channels
		WHERE name IN ('kiro号池', 'Codex 号池', 'jojo-max-claude', 'Gemini 渠道', 'Antigravity 渠道')
	`).Scan(&seededChannels))
	require.Equal(t, 5, seededChannels, "157 seed should execute for an existing installation")

	var seededModels int
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(DISTINCT model_name.value)
		FROM channel_model_pricing AS pricing
		CROSS JOIN LATERAL jsonb_array_elements_text(pricing.models) AS model_name(value)
		WHERE model_name.value IN ('gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna')
	`).Scan(&seededModels))
	require.Equal(t, 3, seededModels, "173 seed should execute for an existing installation")
}
