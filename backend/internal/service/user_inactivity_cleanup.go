package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	userInactivityThreshold    = 30 * 24 * time.Hour
	userInactivityGracePeriod  = 3 * 24 * time.Hour
	userInactivityScanInterval = time.Hour
	userInactivityLockKey      = "user:inactivity-cleanup:leader"
	userInactivityLockTTL      = 10 * time.Minute
	userInactivityBatchSize    = 200
	// InactiveUserCleanupClaimLease is also used by the SQL repository when a
	// worker crashed after claiming a reminder.
	InactiveUserCleanupClaimLease = 10 * time.Minute
)

// InactiveUserCandidate is the activity snapshot used by the cleanup state
// machine. LastActivityAt includes login, authenticated activity, and usage
// logs as calculated by the repository.
type InactiveUserCandidate struct {
	UserID         int64
	Email          string
	Username       string
	LastActivityAt time.Time
}

// InactiveUserCleanupRepository owns only the durable state and activity
// queries for this workflow. It intentionally does not own user deletion.
type InactiveUserCleanupRepository interface {
	ListInactiveUserCandidates(ctx context.Context, cutoff time.Time, limit int) ([]InactiveUserCandidate, error)
	CancelReactivated(ctx context.Context, cutoff time.Time) (int64, error)
	ClaimReminder(ctx context.Context, candidate InactiveUserCandidate, now, deleteAfter time.Time) (bool, error)
	MarkReminderSent(ctx context.Context, candidate InactiveUserCandidate, claimedAt, sentAt, deleteAfter time.Time) error
	ReleaseReminderClaim(ctx context.Context, candidate InactiveUserCandidate, claimedAt time.Time) error
	ListDueInactiveUsers(ctx context.Context, now time.Time, limit int) ([]InactiveUserCandidate, error)
	DeleteState(ctx context.Context, userID int64) error
}

type inactivityNotificationSender interface {
	SendWithReceipt(ctx context.Context, input NotificationEmailSendInput) (time.Time, error)
}

// InactiveUserDeleter is the narrow deletion contract used by the inactivity
// worker. Implementations must re-check activity while holding the user row
// lock and perform the complete existing soft-delete workflow atomically.
type InactiveUserDeleter interface {
	DeleteInactiveUserIfStillInactive(ctx context.Context, id int64, cutoff time.Time) (bool, error)
}

// InactiveUserCleanupService sends one warning after 30 days without activity
// and soft-deletes the account after a further three days without activity.
// The admin deletion path remains the single deletion path so all existing
// user cleanup, API-key tombstoning, and audit semantics are preserved.
type InactiveUserCleanupService struct {
	userRepo     InactiveUserDeleter
	cleanupRepo  InactiveUserCleanupRepository
	notification inactivityNotificationSender
	lockCache    LeaderLockCache
	db           *sql.DB
	instanceID   string
	interval     time.Duration
	threshold    time.Duration
	gracePeriod  time.Duration
	batchSize    int
	startOnce    sync.Once
	stopOnce     sync.Once
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

func NewInactiveUserCleanupService(
	userRepo InactiveUserDeleter,
	cleanupRepo InactiveUserCleanupRepository,
	notification inactivityNotificationSender,
	lockCache LeaderLockCache,
	db *sql.DB,
) *InactiveUserCleanupService {
	return &InactiveUserCleanupService{
		userRepo:     userRepo,
		cleanupRepo:  cleanupRepo,
		notification: notification,
		lockCache:    lockCache,
		db:           db,
		instanceID:   uuid.NewString(),
		interval:     userInactivityScanInterval,
		threshold:    userInactivityThreshold,
		gracePeriod:  userInactivityGracePeriod,
		batchSize:    userInactivityBatchSize,
		stopCh:       make(chan struct{}),
	}
}

func (s *InactiveUserCleanupService) Start() {
	if s == nil || s.userRepo == nil || s.cleanupRepo == nil || s.interval <= 0 {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(s.interval)
			defer ticker.Stop()
			s.runOnce(time.Now().UTC())
			for {
				select {
				case <-ticker.C:
					s.runOnce(time.Now().UTC())
				case <-s.stopCh:
					return
				}
			}
		}()
	})
}

func (s *InactiveUserCleanupService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

// RunOnce executes one cycle. It is exported for deterministic operational
// tests and keeps the scheduler loop itself deliberately small.
func (s *InactiveUserCleanupService) RunOnce(ctx context.Context, now time.Time) error {
	if s == nil || s.userRepo == nil || s.cleanupRepo == nil {
		return errors.New("inactive user cleanup service is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now = now.UTC()
	cutoff := now.Add(-s.threshold)

	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, userInactivityLockKey, s.instanceID, userInactivityLockTTL)
	if !ok {
		return nil
	}
	defer release()

	if _, err := s.cleanupRepo.CancelReactivated(ctx, cutoff); err != nil {
		return fmt.Errorf("cancel reactivated users: %w", err)
	}
	if err := s.sendReminders(ctx, now, cutoff); err != nil {
		return err
	}
	return s.deleteDueUsers(ctx, now)
}

func (s *InactiveUserCleanupService) runOnce(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.RunOnce(ctx, now); err != nil {
		log.Printf("[InactiveUserCleanup] cycle failed: %v", err)
	}
}

func (s *InactiveUserCleanupService) sendReminders(ctx context.Context, now, cutoff time.Time) error {
	if s.notification == nil {
		return errors.New("notification email service is not configured")
	}
	candidates, err := s.cleanupRepo.ListInactiveUserCandidates(ctx, cutoff, s.batchSize)
	if err != nil {
		return fmt.Errorf("list inactive users: %w", err)
	}
	for _, candidate := range candidates {
		deleteAfter := now.Add(s.gracePeriod)
		claimed, err := s.cleanupRepo.ClaimReminder(ctx, candidate, now, deleteAfter)
		if err != nil {
			return fmt.Errorf("claim inactivity reminder for user %d: %w", candidate.UserID, err)
		}
		if !claimed {
			continue
		}
		// Include the activity snapshot in the source identity. If a user later
		// becomes active and goes idle again, that new cycle gets a new key.
		sourceID := fmt.Sprintf("%d:%d", candidate.UserID, candidate.LastActivityAt.UnixNano())
		sentAt, sendErr := s.notification.SendWithReceipt(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventUserInactivityWarning,
			Locale:         "",
			RecipientEmail: candidate.Email,
			RecipientName:  firstNonEmpty(candidate.Username, candidate.Email),
			UserID:         candidate.UserID,
			SourceType:     "user_inactivity_cleanup",
			SourceID:       sourceID,
			ReminderKey:    "30d",
			Variables: map[string]string{
				"inactive_since": candidate.LastActivityAt.Format("2006-01-02 15:04"),
				"deletion_time":  deleteAfter.Format("2006-01-02 15:04"),
				"login_url":      loginURLForNotification(s.notification),
			},
		})
		// Finalize an accepted message even if SMTP outlived the scan timeout or
		// the secondary delivery receipt failed to persist. Never infer a send
		// from nil alone: suppressed/in-flight messages do not start the grace.
		finalizeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if sentAt.IsZero() {
			err = s.cleanupRepo.ReleaseReminderClaim(finalizeCtx, candidate, now)
		} else {
			err = s.cleanupRepo.MarkReminderSent(finalizeCtx, candidate, now, sentAt, sentAt.Add(s.gracePeriod))
		}
		cancel()
		if err != nil || sendErr != nil {
			return fmt.Errorf("finalize inactivity reminder for user %d: %w", candidate.UserID, errors.Join(sendErr, err))
		}
	}
	return nil
}

func (s *InactiveUserCleanupService) deleteDueUsers(ctx context.Context, now time.Time) error {
	due, err := s.cleanupRepo.ListDueInactiveUsers(ctx, now, s.batchSize)
	if err != nil {
		return fmt.Errorf("list due inactive users: %w", err)
	}
	for _, candidate := range due {
		deleted, err := s.userRepo.DeleteInactiveUserIfStillInactive(ctx, candidate.UserID, now.Add(-s.threshold))
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				_ = s.cleanupRepo.DeleteState(ctx, candidate.UserID)
				continue
			}
			return fmt.Errorf("delete inactive user %d: %w", candidate.UserID, err)
		}
		if !deleted {
			// The user became active, was disabled, or was removed between the due
			// query and the locked delete check. Leave no stale cleanup cycle.
			_ = s.cleanupRepo.DeleteState(ctx, candidate.UserID)
			continue
		}
		// The FK cascade normally removes this row. Keep the explicit cleanup for
		// repositories/databases that defer or do not enforce the FK immediately.
		if err := s.cleanupRepo.DeleteState(ctx, candidate.UserID); err != nil {
			return fmt.Errorf("delete inactivity state for user %d: %w", candidate.UserID, err)
		}
	}
	return nil
}

type notificationLoginURLProvider interface {
	LoginURL(context.Context) string
}

func loginURLForNotification(sender inactivityNotificationSender) string {
	if provider, ok := sender.(notificationLoginURLProvider); ok {
		return provider.LoginURL(context.Background())
	}
	return "/login"
}
