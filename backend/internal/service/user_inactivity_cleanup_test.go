package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

type inactivityCleanupRepoStub struct {
	candidates    []InactiveUserCandidate
	due           []InactiveUserCandidate
	claimed       map[int64]bool
	marked        map[int64]bool
	released      map[int64]bool
	deletedStates map[int64]bool
	cancelCalls   int
	claimCalls    int
	listCalls     int
	markedAt      time.Time
	deleteAfter   time.Time
	markErr       error
	finalizeErr   error
}

func (r *inactivityCleanupRepoStub) ListInactiveUserCandidates(context.Context, time.Time, int) ([]InactiveUserCandidate, error) {
	r.listCalls++
	return append([]InactiveUserCandidate(nil), r.candidates...), nil
}

func (r *inactivityCleanupRepoStub) CancelReactivated(context.Context, time.Time) (int64, error) {
	r.cancelCalls++
	return 0, nil
}

func (r *inactivityCleanupRepoStub) ClaimReminder(_ context.Context, candidate InactiveUserCandidate, _, _ time.Time) (bool, error) {
	r.claimCalls++
	if r.claimed == nil {
		r.claimed = map[int64]bool{}
	}
	if r.claimed[candidate.UserID] {
		return false, nil
	}
	r.claimed[candidate.UserID] = true
	return true, nil
}

func (r *inactivityCleanupRepoStub) MarkReminderSent(ctx context.Context, candidate InactiveUserCandidate, _, sentAt, deleteAfter time.Time) error {
	r.finalizeErr = ctx.Err()
	if r.markErr != nil {
		return r.markErr
	}
	if r.marked == nil {
		r.marked = map[int64]bool{}
	}
	r.marked[candidate.UserID] = true
	r.markedAt = sentAt
	r.deleteAfter = deleteAfter
	return nil
}

func (r *inactivityCleanupRepoStub) ReleaseReminderClaim(_ context.Context, candidate InactiveUserCandidate, _ time.Time) error {
	if r.released == nil {
		r.released = map[int64]bool{}
	}
	r.released[candidate.UserID] = true
	delete(r.claimed, candidate.UserID)
	return nil
}

func (r *inactivityCleanupRepoStub) ListDueInactiveUsers(context.Context, time.Time, int) ([]InactiveUserCandidate, error) {
	return append([]InactiveUserCandidate(nil), r.due...), nil
}

func (r *inactivityCleanupRepoStub) DeleteState(_ context.Context, userID int64) error {
	if r.deletedStates == nil {
		r.deletedStates = map[int64]bool{}
	}
	r.deletedStates[userID] = true
	return nil
}

type inactivityDeleterStub struct {
	deleted []int64
	err     error
}

func (r *inactivityDeleterStub) DeleteInactiveUserIfStillInactive(_ context.Context, userID int64, _ time.Time) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	r.deleted = append(r.deleted, userID)
	return true, nil
}

type inactivityNotificationStub struct {
	sent   []NotificationEmailSendInput
	err    error
	sentAt time.Time
	skip   bool
	onSend func()
}

func (s *inactivityNotificationStub) SendWithReceipt(_ context.Context, input NotificationEmailSendInput) (time.Time, error) {
	if s.onSend != nil {
		s.onSend()
	}
	if s.skip || (s.err != nil && s.sentAt.IsZero()) {
		return time.Time{}, s.err
	}
	s.sent = append(s.sent, input)
	if s.sentAt.IsZero() {
		return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC), s.err
	}
	return s.sentAt, s.err
}

func newInactivityCleanupTestService(repo *inactivityCleanupRepoStub, deleter *inactivityDeleterStub, sender inactivityNotificationSender) *InactiveUserCleanupService {
	svc := NewInactiveUserCleanupService(deleter, repo, sender, nil, nil)
	svc.threshold = 30 * 24 * time.Hour
	svc.gracePeriod = 3 * 24 * time.Hour
	svc.batchSize = 200
	return svc
}

func TestInactiveUserCleanupService_RemindsOnceAndDeletesAfterGracePeriod(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	repo := &inactivityCleanupRepoStub{candidates: []InactiveUserCandidate{{
		UserID: 7, Email: "idle@example.com", Username: "Idle", LastActivityAt: now.Add(-31 * 24 * time.Hour),
	}}}
	deleter := &inactivityDeleterStub{}
	sender := &inactivityNotificationStub{}
	svc := newInactivityCleanupTestService(repo, deleter, sender)

	require.NoError(t, svc.RunOnce(context.Background(), now))
	require.Len(t, sender.sent, 1)
	require.Empty(t, deleter.deleted)
	require.True(t, repo.marked[7])
	require.Equal(t, "30d", sender.sent[0].ReminderKey)
	require.Equal(t, "2026-09-08 12:00", sender.sent[0].Variables["deletion_time"])

	// A second scan sees the durable claim and must not send a second message.
	require.NoError(t, svc.RunOnce(context.Background(), now.Add(time.Hour)))
	require.Len(t, sender.sent, 1)

	// The due list is populated by the repository only after the three-day grace period.
	repo.candidates = nil
	repo.due = []InactiveUserCandidate{{UserID: 7, Email: "idle@example.com", LastActivityAt: now.Add(-31 * 24 * time.Hour)}}
	require.NoError(t, svc.RunOnce(context.Background(), now.Add(3*24*time.Hour)))
	require.Equal(t, []int64{7}, deleter.deleted)
	require.True(t, repo.deletedStates[7])
}

func TestInactiveUserCleanupService_SendFailureReleasesClaim(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	repo := &inactivityCleanupRepoStub{candidates: []InactiveUserCandidate{{UserID: 8, Email: "idle@example.com", LastActivityAt: now.Add(-31 * 24 * time.Hour)}}}
	sender := &inactivityNotificationStub{err: errors.New("smtp unavailable")}
	svc := newInactivityCleanupTestService(repo, &inactivityDeleterStub{}, sender)

	err := svc.RunOnce(context.Background(), now)
	require.Error(t, err)
	require.True(t, repo.released[8])
	require.False(t, repo.marked[8])
}

func TestInactiveUserCleanupService_LeaderLockSkipsPeer(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	repo := &inactivityCleanupRepoStub{candidates: []InactiveUserCandidate{{UserID: 9, Email: "idle@example.com", LastActivityAt: now.Add(-31 * 24 * time.Hour)}}}
	deleter := &inactivityDeleterStub{}
	sender := &inactivityNotificationStub{}
	cache := &fakeLeaderLockCache{}
	peerRelease, ok := tryAcquireSingletonLeaderLock(context.Background(), cache, nil, userInactivityLockKey, "peer", time.Minute)
	require.True(t, ok)
	defer peerRelease()

	svc := newInactivityCleanupTestService(repo, deleter, sender)
	svc.lockCache = cache
	require.NoError(t, svc.RunOnce(context.Background(), now))
	require.Zero(t, repo.cancelCalls)
	require.Empty(t, sender.sent)
}

func TestInactiveUserCleanupService_DeleteErrorKeepsStateForRetry(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	repo := &inactivityCleanupRepoStub{due: []InactiveUserCandidate{{UserID: 10, Email: "idle@example.com", LastActivityAt: now.Add(-31 * 24 * time.Hour)}}}
	deleter := &inactivityDeleterStub{err: errors.New("db unavailable")}
	svc := newInactivityCleanupTestService(repo, deleter, &inactivityNotificationStub{})

	err := svc.RunOnce(context.Background(), now)
	require.Error(t, err)
	require.Empty(t, repo.deletedStates)
}

func TestInactiveUserCleanupService_UnconfirmedSendDoesNotStartGrace(t *testing.T) {
	now := time.Now().UTC()
	repo := &inactivityCleanupRepoStub{candidates: []InactiveUserCandidate{{UserID: 8, Email: "idle@example.com", LastActivityAt: now.Add(-31 * 24 * time.Hour)}}}
	sender := &inactivityNotificationStub{skip: true}
	deleter := &inactivityDeleterStub{}
	svc := newInactivityCleanupTestService(repo, deleter, sender)
	for _, scan := range []time.Time{now, now.Add(4 * 24 * time.Hour)} {
		require.NoError(t, svc.RunOnce(context.Background(), scan))
		require.Empty(t, repo.marked)
		require.Empty(t, deleter.deleted)
		require.True(t, repo.released[8])
	}
}

func TestInactiveUserCleanupService_GraceUsesReceiptDespitePersistenceErrorAndCancellation(t *testing.T) {
	now := time.Now().UTC()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &inactivityCleanupRepoStub{candidates: []InactiveUserCandidate{{UserID: 8, Email: "idle@example.com", LastActivityAt: now.Add(-31 * 24 * time.Hour)}}}
	sentAt := now.Add(45 * time.Second)
	sender := &inactivityNotificationStub{sentAt: sentAt, err: errors.New("receipt persistence failed"), onSend: cancel}
	svc := newInactivityCleanupTestService(repo, &inactivityDeleterStub{}, sender)
	require.ErrorContains(t, svc.RunOnce(ctx, now), "receipt persistence failed")
	require.True(t, repo.marked[8])
	require.False(t, repo.released[8])
	require.Equal(t, sentAt, repo.markedAt)
	require.Equal(t, sentAt.Add(72*time.Hour), repo.deleteAfter)
	require.NoError(t, repo.finalizeErr)
}

func TestInactiveUserCleanupService_RetryRecoversDurableReceiptWithoutResending(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	smtpServer := startNotificationEmailTestSMTPServer(t)
	settings := newNotificationEmailMemorySettingRepo()
	require.NoError(t, settings.SetMultiple(ctx, smtpServer.settings()))
	sender := NewNotificationEmailService(settings, NewEmailService(settings, nil))
	repo := &inactivityCleanupRepoStub{
		candidates: []InactiveUserCandidate{{UserID: 8, Email: "idle@example.com", LastActivityAt: now.Add(-31 * 24 * time.Hour)}},
		markErr:    errors.New("cleanup persistence failed"),
	}
	svc := newInactivityCleanupTestService(repo, &inactivityDeleterStub{}, sender)
	require.ErrorContains(t, svc.RunOnce(ctx, now), "cleanup persistence failed")
	require.Equal(t, int64(1), smtpServer.messageCount())
	require.False(t, repo.released[8])
	// Simulate recovery after the durable claim lease expired.
	repo.claimed = nil
	repo.markErr = nil
	retryAt := now.Add(time.Hour)
	require.NoError(t, svc.RunOnce(ctx, retryAt))
	require.Equal(t, int64(1), smtpServer.messageCount())
	require.True(t, repo.marked[8])
	require.True(t, repo.markedAt.Before(retryAt))
	require.Equal(t, repo.markedAt.Add(72*time.Hour), repo.deleteAfter)
}

func TestInactiveUserDeletion_RechecksActivityAndReminderAfterLocks(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name           string
		lastActive     time.Time
		warnedActivity time.Time
		hasReminder    bool
	}{
		{"recent login", now, now.Add(-40 * 24 * time.Hour), true},
		{"activity after warning still older than cutoff", now.Add(-31 * 24 * time.Hour), now.Add(-40 * 24 * time.Hour), true},
		{"cancelled or unconfirmed or grace not due", time.Time{}, time.Time{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			svc := &adminServiceImpl{entClient: client}
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT u.role, u.status, u.deleted_at FROM users u WHERE u.id = \$1 FOR UPDATE`).
				WithArgs(int64(8)).WillReturnRows(sqlmock.NewRows([]string{"role", "status", "deleted_at"}).AddRow(RoleUser, StatusActive, nil))
			mock.ExpectQuery(`SELECT id, key, deleted_at FROM api_keys WHERE user_id = \$1 ORDER BY id FOR UPDATE`).
				WithArgs(int64(8)).WillReturnRows(sqlmock.NewRows([]string{"id", "key", "deleted_at"}))
			rows := sqlmock.NewRows([]string{"last_activity", "activity_at"})
			if tc.hasReminder {
				rows.AddRow(tc.lastActive, tc.warnedActivity)
			}
			mock.ExpectQuery(`SELECT GREATEST\(.+MAX\(ak.last_used_at\).+MAX\(ul.created_at\).+JOIN user_inactivity_cleanups c.+c.reminder_status = 'sent'.+c.reminder_sent_at IS NOT NULL.+c.reminder_sent_at <= CURRENT_TIMESTAMP - INTERVAL '72 hours'.+c.delete_after <= CURRENT_TIMESTAMP FOR UPDATE OF c`).
				WithArgs(int64(8)).WillReturnRows(rows)
			mock.ExpectRollback()
			deleted, err := svc.DeleteInactiveUserIfStillInactive(context.Background(), 8, now.Add(-30*24*time.Hour))
			require.NoError(t, err)
			require.False(t, deleted)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

type inactivityAtomicUserRepo struct {
	UserRepository
	deleteFn func(context.Context, int64) error
}

func (r *inactivityAtomicUserRepo) Delete(ctx context.Context, id int64) error {
	return r.deleteFn(ctx, id)
}

type inactivityAtomicKeyRepo struct {
	APIKeyRepository
	deleteFn func(context.Context, int64) error
}

func (r *inactivityAtomicKeyRepo) DeleteWithAudit(ctx context.Context, id int64) error {
	return r.deleteFn(ctx, id)
}

func TestInactiveUserDeletion_PreservesAtomicUserAndKeyDeletion(t *testing.T) {
	for _, failAt := range []string{"", "key", "user"} {
		t.Run("failure="+failAt, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			var deletionTx *dbent.Tx
			var calls []string
			svc := &adminServiceImpl{
				entClient: client,
				apiKeyRepo: &inactivityAtomicKeyRepo{deleteFn: func(ctx context.Context, id int64) error {
					require.Equal(t, int64(11), id)
					deletionTx = dbent.TxFromContext(ctx)
					require.NotNil(t, deletionTx)
					calls = append(calls, "key")
					if failAt == "key" {
						return errors.New("key deletion failed")
					}
					return nil
				}},
				userRepo: &inactivityAtomicUserRepo{deleteFn: func(ctx context.Context, id int64) error {
					require.Equal(t, int64(8), id)
					require.Same(t, deletionTx, dbent.TxFromContext(ctx))
					calls = append(calls, "user")
					if failAt == "user" {
						return errors.New("user deletion failed")
					}
					return nil
				}},
			}
			now := time.Now().UTC()
			activity := now.Add(-40 * 24 * time.Hour)
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT u.role, u.status, u.deleted_at FROM users u WHERE u.id = \$1 FOR UPDATE`).
				WithArgs(int64(8)).WillReturnRows(sqlmock.NewRows([]string{"role", "status", "deleted_at"}).AddRow(RoleUser, StatusActive, nil))
			mock.ExpectQuery(`SELECT id, key, deleted_at FROM api_keys WHERE user_id = \$1 ORDER BY id FOR UPDATE`).
				WithArgs(int64(8)).WillReturnRows(sqlmock.NewRows([]string{"id", "key", "deleted_at"}).
				AddRow(11, "test-active", nil).AddRow(12, "test-tombstone", activity))
			mock.ExpectQuery(`SELECT GREATEST\(.+JOIN user_inactivity_cleanups c.+FOR UPDATE OF c`).
				WithArgs(int64(8)).WillReturnRows(sqlmock.NewRows([]string{"last_activity", "activity_at"}).AddRow(activity, activity))
			if failAt == "" {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			deleted, err := svc.DeleteInactiveUserIfStillInactive(context.Background(), 8, now.Add(-30*24*time.Hour))
			if failAt == "" {
				require.NoError(t, err)
				require.True(t, deleted)
			} else {
				require.Error(t, err)
				require.False(t, deleted)
			}
			if failAt == "key" {
				require.Equal(t, []string{"key"}, calls)
			} else {
				require.Equal(t, []string{"key", "user"}, calls)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
