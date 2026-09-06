package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestInactiveUserCleanupRepository_ClaimRequiresMatchingActivityAndRecipient(t *testing.T) {
	for _, claimed := range []bool{true, false} {
		t.Run(map[bool]string{true: "unchanged", false: "activity or recipient changed"}[claimed], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := NewInactiveUserCleanupRepository(db)
			now := time.Now().UTC()
			candidate := service.InactiveUserCandidate{UserID: 8, Email: "idle@example.com", LastActivityAt: now.Add(-31 * 24 * time.Hour)}
			rows := sqlmock.NewRows([]string{"user_id"})
			if claimed {
				rows.AddRow(candidate.UserID)
			}
			mock.ExpectQuery(`INSERT INTO user_inactivity_cleanups.+u.role <> 'admin'.+u.status = 'active'.+GREATEST\(.+ < \$4 AND GREATEST\(.+ = \$6 AND u.email = \$7 ON CONFLICT.+reminder_sent_at IS NULL.+reminder_claimed_at < \$5\) RETURNING user_id`).
				WithArgs(candidate.UserID, now, now.Add(72*time.Hour), now.Add(-30*24*time.Hour), now.Add(-service.InactiveUserCleanupClaimLease), candidate.LastActivityAt, candidate.Email).
				WillReturnRows(rows)
			actual, err := repo.ClaimReminder(context.Background(), candidate, now, now.Add(72*time.Hour))
			require.NoError(t, err)
			require.Equal(t, claimed, actual)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestInactiveUserCleanupRepository_FinalizationIsBoundToClaimAndReceipt(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewInactiveUserCleanupRepository(db)
	ctx := context.Background()
	now := time.Now().UTC()
	candidate := service.InactiveUserCandidate{UserID: 8, LastActivityAt: now.Add(-31 * 24 * time.Hour)}
	sentAt := now.Add(45 * time.Second)
	deadline := sentAt.Add(72 * time.Hour)
	for _, affected := range []int64{1, 0} {
		mock.ExpectExec(`UPDATE user_inactivity_cleanups SET reminder_status = 'sent', reminder_sent_at = \$2, delete_after = \$3.+WHERE user_id = \$1 AND reminder_status = 'sending' AND reminder_sent_at IS NULL AND activity_at = \$4 AND reminder_claimed_at = \$5`).
			WithArgs(candidate.UserID, sentAt, deadline, candidate.LastActivityAt, now).
			WillReturnResult(sqlmock.NewResult(0, affected))
		require.NoError(t, repo.MarkReminderSent(ctx, candidate, now, sentAt, deadline))
		mock.ExpectExec(`UPDATE user_inactivity_cleanups SET reminder_status = 'pending'.+WHERE user_id = \$1 AND reminder_status = 'sending' AND reminder_sent_at IS NULL AND activity_at = \$2 AND reminder_claimed_at = \$3`).
			WithArgs(candidate.UserID, candidate.LastActivityAt, now).
			WillReturnResult(sqlmock.NewResult(0, affected))
		require.NoError(t, repo.ReleaseReminderClaim(ctx, candidate, now))
	}
	require.Error(t, repo.MarkReminderSent(ctx, candidate, now, time.Time{}, deadline))
	require.Error(t, repo.MarkReminderSent(ctx, candidate, now, sentAt, deadline.Add(-time.Nanosecond)))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestInactiveUserCleanupRepository_DueRequiresSentStatusFullGraceAndUnchangedActivity(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewInactiveUserCleanupRepository(db)
	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT u.id, u.email, u.username, GREATEST\(.+WHERE c.reminder_sent_at IS NOT NULL AND c.reminder_status = 'sent' AND c.reminder_sent_at <= \(\$1::timestamptz - INTERVAL '72 hours'\) AND c.delete_after <= \$1.+GREATEST\(.+ < \(\$1 - INTERVAL '30 days'\) AND GREATEST\(.+ <= c.activity_at ORDER BY c.delete_after ASC, u.id ASC LIMIT \$2`).
		WithArgs(now, 200).WillReturnRows(sqlmock.NewRows([]string{"id", "email", "username", "last_activity_at"}).
		AddRow(8, "idle@example.com", "Idle", now.Add(-34*24*time.Hour)))
	due, err := repo.ListDueInactiveUsers(context.Background(), now, 0)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, int64(8), due[0].UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestInactiveUserCleanupRepository_CancelsAllActivitySourcesEvenAfterLongOutage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewInactiveUserCleanupRepository(db)
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	mock.ExpectExec(`DELETE FROM user_inactivity_cleanups c USING users u.+u.deleted_at IS NOT NULL OR u.role = 'admin' OR u.status <> 'active' OR GREATEST\(.+u.last_login_at.+u.last_active_at.+MAX\(ak.last_used_at\).+MAX\(ul.created_at\).+ >= \$1 OR GREATEST\(.+ > c.activity_at`).
		WithArgs(cutoff).WillReturnResult(sqlmock.NewResult(0, 2))
	cancelled, err := repo.CancelReactivated(context.Background(), cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(2), cancelled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestInactiveUserCleanupRepository_CandidatesExcludeWarnedProtectedAndRecentUsers(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewInactiveUserCleanupRepository(db)
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	mock.ExpectQuery(`SELECT u.id, u.email, u.username, GREATEST\(.+u.deleted_at IS NULL AND u.role <> 'admin' AND u.status = 'active'.+linuxdo-connect.invalid.+oidc-connect.invalid.+wechat-connect.invalid.+dingtalk-connect.invalid.+GREATEST\(.+ < \$1 AND NOT EXISTS \(.+c.reminder_sent_at IS NOT NULL.+ORDER BY last_activity_at ASC, u.id ASC LIMIT \$2`).
		WithArgs(cutoff, 200).WillReturnRows(sqlmock.NewRows([]string{"id", "email", "username", "last_activity_at"}))
	candidates, err := repo.ListInactiveUserCandidates(context.Background(), cutoff, 0)
	require.NoError(t, err)
	require.Empty(t, candidates)
	require.NoError(t, mock.ExpectationsWereMet())
}
