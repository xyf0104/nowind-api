package service

import (
	"context"
	"errors"
	"testing"
	"time"

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

func (r *inactivityCleanupRepoStub) MarkReminderSent(_ context.Context, userID int64, _ time.Time) error {
	if r.marked == nil {
		r.marked = map[int64]bool{}
	}
	r.marked[userID] = true
	return nil
}

func (r *inactivityCleanupRepoStub) ReleaseReminderClaim(_ context.Context, userID int64) error {
	if r.released == nil {
		r.released = map[int64]bool{}
	}
	r.released[userID] = true
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
	sent []NotificationEmailSendInput
	err  error
}

func (s *inactivityNotificationStub) Send(_ context.Context, input NotificationEmailSendInput) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, input)
	return nil
}

func newInactivityCleanupTestService(repo *inactivityCleanupRepoStub, deleter *inactivityDeleterStub, sender *inactivityNotificationStub) *InactiveUserCleanupService {
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
