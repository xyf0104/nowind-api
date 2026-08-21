//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const allowlistMigrationNotificationChannel = "xiass_user_group_account_allowlist"

func TestUserGroupAccountAllowlistMigrationsExecuteAndMaintainSoftDeletes(t *testing.T) {
	ctx := context.Background()
	schemaName := "allowlist_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pq.QuoteIdentifier(schemaName)

	_, err := integrationDB.ExecContext(ctx, "CREATE SCHEMA "+quotedSchema)
	require.NoError(t, err)

	conn, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "RESET search_path")
		_ = conn.Close()
		_, _ = integrationDB.ExecContext(
			context.Background(),
			"DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE",
		)
	})

	_, err = conn.ExecContext(ctx, "SET search_path TO "+quotedSchema)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `
CREATE TABLE users (
    id BIGINT PRIMARY KEY,
    deleted_at TIMESTAMPTZ
);
CREATE TABLE groups (
    id BIGINT PRIMARY KEY,
    deleted_at TIMESTAMPTZ
);
CREATE TABLE accounts (
    id BIGINT PRIMARY KEY,
    deleted_at TIMESTAMPTZ
);
`)
	require.NoError(t, err)

	migration231, err := dbmigrations.FS.ReadFile("231_user_group_account_allowlists.sql")
	require.NoError(t, err)
	migration233, err := dbmigrations.FS.ReadFile("233_user_group_account_allowlist_soft_delete.sql")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(migration231))
	require.NoError(t, err)

	const (
		activeUserID     int64 = 810000000001
		deletedUserID    int64 = 810000000002
		activeGroupID    int64 = 820000000001
		deletedGroupID   int64 = 820000000002
		activeAccountID  int64 = 830000000001
		deletedAccountID int64 = 830000000002
	)

	_, err = conn.ExecContext(ctx, `
INSERT INTO users (id, deleted_at) VALUES
    ($1, NULL),
    ($2, NOW());
INSERT INTO groups (id, deleted_at) VALUES
    ($3, NULL),
    ($4, NOW());
INSERT INTO accounts (id, deleted_at) VALUES
    ($5, NULL),
    ($6, NOW());
INSERT INTO user_group_account_allowlist_scopes (user_id, group_id) VALUES
    ($1, $3),
    ($2, $3),
    ($1, $4);
INSERT INTO user_group_account_allowlists (user_id, group_id, account_id) VALUES
    ($1, $3, $6),
    ($2, $3, $5),
    ($1, $4, $5);
`, activeUserID, deletedUserID, activeGroupID, deletedGroupID, activeAccountID, deletedAccountID)
	require.NoError(t, err)

	// Simulate an earlier development draft that created the detail table but did
	// not include the account_id reverse index. Migration 233 must repair it.
	_, err = conn.ExecContext(ctx, "DROP INDEX idx_user_group_account_allowlists_account_id")
	require.NoError(t, err)

	listener := pq.NewListener(integrationPostgresDSN, 50*time.Millisecond, time.Second, nil)
	require.NoError(t, listener.Listen(allowlistMigrationNotificationChannel))
	require.NoError(t, listener.Ping())
	t.Cleanup(func() { _ = listener.Close() })

	cleanupPayloads := map[string]struct{}{
		fmt.Sprintf("%d:%d", activeUserID, activeGroupID):  {},
		fmt.Sprintf("%d:%d", deletedUserID, activeGroupID): {},
		fmt.Sprintf("%d:%d", activeUserID, deletedGroupID): {},
	}

	tx, err := conn.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migration233))
	require.NoError(t, err)

	requireRowCount(t, tx, 1, `
SELECT COUNT(*)
FROM user_group_account_allowlist_scopes
WHERE user_id = $1 AND group_id = $2
`, activeUserID, activeGroupID)
	requireRowCount(t, tx, 0, `
SELECT COUNT(*)
FROM user_group_account_allowlists
WHERE user_id = $1 AND group_id = $2
`, activeUserID, activeGroupID)
	requireRowCount(t, tx, 0, `
SELECT COUNT(*)
FROM user_group_account_allowlist_scopes
WHERE (user_id = $1 AND group_id = $2)
   OR (user_id = $3 AND group_id = $4)
`, deletedUserID, activeGroupID, activeUserID, deletedGroupID)

	var indexExists bool
	err = tx.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM pg_indexes
    WHERE schemaname = current_schema()
      AND indexname = 'idx_user_group_account_allowlists_account_id'
)
`).Scan(&indexExists)
	require.NoError(t, err)
	require.True(t, indexExists)

	requireNoMatchingAllowlistNotifications(t, listener, cleanupPayloads, 200*time.Millisecond)
	require.NoError(t, tx.Commit())
	requireMatchingAllowlistNotifications(t, listener, cleanupPayloads, 3*time.Second)

	// Re-running the follow-up migration is safe and leaves the repaired state
	// intact, which also protects manual recovery workflows.
	_, err = conn.ExecContext(ctx, string(migration233))
	require.NoError(t, err)

	const (
		accountUserID        int64 = 810000000101
		deletedParentUserID  int64 = 810000000102
		accountGroupID       int64 = 820000000101
		deletedParentGroupID int64 = 820000000102
		softDeletedAccountID int64 = 830000000101
		remainingAccountID   int64 = 830000000102
	)

	_, err = conn.ExecContext(ctx, `
INSERT INTO users (id, deleted_at) VALUES ($1, NULL), ($2, NULL);
INSERT INTO groups (id, deleted_at) VALUES ($3, NULL), ($4, NULL);
INSERT INTO accounts (id, deleted_at) VALUES ($5, NULL), ($6, NULL);
INSERT INTO user_group_account_allowlist_scopes (user_id, group_id) VALUES
    ($1, $3),
    ($2, $3),
    ($1, $4);
INSERT INTO user_group_account_allowlists (user_id, group_id, account_id) VALUES
    ($1, $3, $5),
    ($2, $3, $6),
    ($1, $4, $6);
`, accountUserID, deletedParentUserID, accountGroupID, deletedParentGroupID, softDeletedAccountID, remainingAccountID)
	require.NoError(t, err)

	triggerPayloads := map[string]struct{}{
		fmt.Sprintf("%d:%d", accountUserID, accountGroupID):       {},
		fmt.Sprintf("%d:%d", deletedParentUserID, accountGroupID): {},
		fmt.Sprintf("%d:%d", accountUserID, deletedParentGroupID): {},
	}
	requireMatchingAllowlistNotifications(t, listener, triggerPayloads, 3*time.Second)

	tx, err = conn.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET deleted_at = NOW() WHERE id = $1`, softDeletedAccountID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE users SET deleted_at = NOW() WHERE id = $1`, deletedParentUserID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE groups SET deleted_at = NOW() WHERE id = $1`, deletedParentGroupID)
	require.NoError(t, err)

	requireRowCount(t, tx, 1, `
SELECT COUNT(*)
FROM user_group_account_allowlist_scopes
WHERE user_id = $1 AND group_id = $2
`, accountUserID, accountGroupID)
	requireRowCount(t, tx, 0, `
SELECT COUNT(*)
FROM user_group_account_allowlists
WHERE user_id = $1 AND group_id = $2
`, accountUserID, accountGroupID)
	requireRowCount(t, tx, 0, `
SELECT COUNT(*)
FROM user_group_account_allowlist_scopes
WHERE (user_id = $1 AND group_id = $2)
   OR (user_id = $3 AND group_id = $4)
`, deletedParentUserID, accountGroupID, accountUserID, deletedParentGroupID)

	requireNoMatchingAllowlistNotifications(t, listener, triggerPayloads, 200*time.Millisecond)
	require.NoError(t, tx.Commit())
	requireMatchingAllowlistNotifications(t, listener, triggerPayloads, 3*time.Second)
}

type allowlistMigrationQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func requireRowCount(t *testing.T, queryer allowlistMigrationQueryer, want int, query string, args ...any) {
	t.Helper()
	var got int
	require.NoError(t, queryer.QueryRowContext(context.Background(), query, args...).Scan(&got))
	require.Equal(t, want, got)
}

func requireNoMatchingAllowlistNotifications(
	t *testing.T,
	listener *pq.Listener,
	payloads map[string]struct{},
	duration time.Duration,
) {
	t.Helper()
	deadline := time.NewTimer(duration)
	defer deadline.Stop()

	for {
		select {
		case notification := <-listener.Notify:
			if notification == nil || notification.Channel != allowlistMigrationNotificationChannel {
				continue
			}
			if _, matches := payloads[notification.Extra]; matches {
				t.Fatalf("allowlist notification %q was visible before transaction commit", notification.Extra)
			}
		case <-deadline.C:
			return
		}
	}
}

func requireMatchingAllowlistNotifications(
	t *testing.T,
	listener *pq.Listener,
	payloads map[string]struct{},
	timeout time.Duration,
) {
	t.Helper()
	pending := make(map[string]struct{}, len(payloads))
	for payload := range payloads {
		pending[payload] = struct{}{}
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for len(pending) > 0 {
		select {
		case notification := <-listener.Notify:
			if notification == nil || notification.Channel != allowlistMigrationNotificationChannel {
				continue
			}
			delete(pending, notification.Extra)
		case <-deadline.C:
			t.Fatalf("timed out waiting for allowlist notifications: %v", pending)
		}
	}
}
