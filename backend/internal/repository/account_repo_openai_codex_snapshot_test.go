package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestUpdateOpenAICodexSnapshotAppliesOnlyNotEarlierSnapshots(t *testing.T) {
	tests := []struct {
		name     string
		affected int64
		applied  bool
	}{
		{name: "newer or equal snapshot", affected: 1, applied: true},
		{name: "older snapshot", affected: 0, applied: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })

			updatedAt := time.Date(2026, time.August, 14, 9, 30, 0, 123456000, time.UTC)
			mock.ExpectExec(regexp.QuoteMeta(updateOpenAICodexSnapshotQuery)).
				WithArgs(sqlmock.AnyArg(), int64(42), updatedAt).
				WillReturnResult(sqlmock.NewResult(0, tt.affected))
			repo := newAccountRepositoryWithSQL(client, db, nil)

			applied, err := repo.UpdateOpenAICodexSnapshot(context.Background(), 42, map[string]any{
				"codex_usage_updated_at": updatedAt.Format(time.RFC3339Nano),
				"codex_7d_used_percent":  13.0,
				"codex_7d_reset_at":      "2026-08-20T09:30:00Z",
			})

			require.NoError(t, err)
			require.Equal(t, tt.applied, applied)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}

	require.Contains(t, updateOpenAICodexSnapshotQuery, "COALESCE(extra, '{}'::jsonb) || $1::jsonb")
	require.Contains(t, updateOpenAICodexSnapshotQuery, "codex_usage_updated_at")
	require.Contains(t, updateOpenAICodexSnapshotQuery, "::timestamptz <= $3::timestamptz")
}

func TestUpdateOpenAICodexSnapshotRejectsMissingOrInvalidTimestampBeforeSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)

	for _, updates := range []map[string]any{
		{"codex_7d_used_percent": 13.0},
		{"codex_usage_updated_at": "not-a-time", "codex_7d_used_percent": 13.0},
		{"codex_usage_updated_at": 123, "codex_7d_used_percent": 13.0},
	} {
		applied, err := repo.UpdateOpenAICodexSnapshot(context.Background(), 42, updates)
		require.Error(t, err)
		require.False(t, applied)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateExtraOrdersCodexSnapshotWithoutDroppingOtherExtraFields(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)

	updatedAt := time.Date(2026, time.August, 14, 9, 30, 0, 123456000, time.UTC)
	expectedExpression := "COALESCE(extra, '{}'::jsonb)" +
		" || COALESCE($1::jsonb -> 'other', '{}'::jsonb)" +
		" || CASE WHEN COALESCE((" + openAICodexCurrentTimestampExpression + ")::timestamptz" +
		" <= (($1::jsonb #>> '{codex,codex_usage_updated_at}')::timestamptz), TRUE)" +
		" THEN COALESCE($1::jsonb -> 'codex', '{}'::jsonb) ELSE '{}'::jsonb END"
	mock.ExpectExec(regexp.QuoteMeta("UPDATE accounts SET extra = "+expectedExpression+", updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL")).
		WithArgs(sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.UpdateExtra(context.Background(), 42, map[string]any{
		"codex_usage_updated_at":     updatedAt.Format(time.RFC3339Nano),
		"codex_7d_used_percent":      13.0,
		"session_window_utilization": 0.42,
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
