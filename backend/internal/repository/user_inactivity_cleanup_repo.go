package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type userInactivityCleanupRepository struct {
	db *sql.DB
}

func NewInactiveUserCleanupRepository(db *sql.DB) service.InactiveUserCleanupRepository {
	return &userInactivityCleanupRepository{db: db}
}

const userInactivityActivitySQL = `GREATEST(
    u.created_at,
    COALESCE(u.last_login_at, '-infinity'::timestamptz),
    COALESCE(u.last_active_at, '-infinity'::timestamptz),
    COALESCE((SELECT MAX(ak.last_used_at) FROM api_keys ak WHERE ak.user_id = u.id), '-infinity'::timestamptz),
    COALESCE((SELECT MAX(ul.created_at) FROM usage_logs ul WHERE ul.user_id = u.id), '-infinity'::timestamptz)
)`

func (r *userInactivityCleanupRepository) ListInactiveUserCandidates(ctx context.Context, cutoff time.Time, limit int) ([]service.InactiveUserCandidate, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database is unavailable")
	}
	if limit <= 0 {
		limit = 200
	}
	query := fmt.Sprintf(`
SELECT u.id, u.email, u.username, %s AS last_activity_at
FROM users u
WHERE u.deleted_at IS NULL
  AND u.role <> 'admin'
  AND u.status = 'active'
  AND BTRIM(u.email) <> ''
  AND LOWER(BTRIM(u.email)) NOT LIKE '%%@linuxdo-connect.invalid'
  AND LOWER(BTRIM(u.email)) NOT LIKE '%%@oidc-connect.invalid'
  AND LOWER(BTRIM(u.email)) NOT LIKE '%%@wechat-connect.invalid'
  AND LOWER(BTRIM(u.email)) NOT LIKE '%%@dingtalk-connect.invalid'
  AND %s < $1
  AND NOT EXISTS (
      SELECT 1
      FROM user_inactivity_cleanups c
      WHERE c.user_id = u.id AND c.reminder_sent_at IS NOT NULL
  )
ORDER BY last_activity_at ASC, u.id ASC
LIMIT $2`, userInactivityActivitySQL, userInactivityActivitySQL)
	return r.queryCandidates(ctx, query, cutoff, limit)
}

func (r *userInactivityCleanupRepository) CancelReactivated(ctx context.Context, cutoff time.Time) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("database is unavailable")
	}
	query := fmt.Sprintf(`
DELETE FROM user_inactivity_cleanups c
USING users u
WHERE c.user_id = u.id
  AND (
      u.deleted_at IS NOT NULL
      OR u.role = 'admin'
      OR u.status <> 'active'
      OR %s >= $1
      OR %s > c.activity_at
  )`, userInactivityActivitySQL, userInactivityActivitySQL)
	result, err := r.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *userInactivityCleanupRepository) ClaimReminder(ctx context.Context, candidate service.InactiveUserCandidate, now, deleteAfter time.Time) (bool, error) {
	if r == nil || r.db == nil {
		return false, fmt.Errorf("database is unavailable")
	}
	cutoff := now.Add(-30 * 24 * time.Hour)
	query := fmt.Sprintf(`
INSERT INTO user_inactivity_cleanups
    (user_id, activity_at, reminder_status, reminder_claimed_at, delete_after, created_at, updated_at)
SELECT u.id, %s, 'sending', $2, $3, $2, $2
FROM users u
WHERE u.id = $1
  AND u.deleted_at IS NULL
  AND u.role <> 'admin'
  AND u.status = 'active'
  AND BTRIM(u.email) <> ''
  AND LOWER(BTRIM(u.email)) NOT LIKE '%%@linuxdo-connect.invalid'
  AND LOWER(BTRIM(u.email)) NOT LIKE '%%@oidc-connect.invalid'
  AND LOWER(BTRIM(u.email)) NOT LIKE '%%@wechat-connect.invalid'
  AND LOWER(BTRIM(u.email)) NOT LIKE '%%@dingtalk-connect.invalid'
  AND %s < $4
  AND %s = $6
  AND u.email = $7
ON CONFLICT (user_id) DO UPDATE SET
    activity_at = EXCLUDED.activity_at,
    reminder_status = 'sending',
    reminder_claimed_at = EXCLUDED.reminder_claimed_at,
    delete_after = EXCLUDED.delete_after,
    updated_at = EXCLUDED.updated_at
WHERE user_inactivity_cleanups.reminder_sent_at IS NULL
  AND (user_inactivity_cleanups.reminder_claimed_at IS NULL OR user_inactivity_cleanups.reminder_claimed_at < $5)
RETURNING user_id`, userInactivityActivitySQL, userInactivityActivitySQL, userInactivityActivitySQL)
	var userID int64
	err := r.db.QueryRowContext(ctx, query, candidate.UserID, now, deleteAfter, cutoff, now.Add(-service.InactiveUserCleanupClaimLease), candidate.LastActivityAt, candidate.Email).Scan(&userID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (r *userInactivityCleanupRepository) MarkReminderSent(ctx context.Context, candidate service.InactiveUserCandidate, claimedAt, sentAt, deleteAfter time.Time) error {
	if sentAt.IsZero() || deleteAfter.Before(sentAt.Add(3*24*time.Hour)) {
		return fmt.Errorf("confirmed delivery and full inactivity grace period are required")
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE user_inactivity_cleanups
SET reminder_status = 'sent', reminder_sent_at = $2, delete_after = $3, reminder_claimed_at = NULL, updated_at = NOW()
WHERE user_id = $1 AND reminder_status = 'sending'
  AND reminder_sent_at IS NULL AND activity_at = $4 AND reminder_claimed_at = $5`, candidate.UserID, sentAt, deleteAfter, candidate.LastActivityAt, claimedAt)
	return err
}

func (r *userInactivityCleanupRepository) ReleaseReminderClaim(ctx context.Context, candidate service.InactiveUserCandidate, claimedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
UPDATE user_inactivity_cleanups
SET reminder_status = 'pending', reminder_claimed_at = NULL, updated_at = NOW()
WHERE user_id = $1 AND reminder_status = 'sending'
  AND reminder_sent_at IS NULL AND activity_at = $2 AND reminder_claimed_at = $3`, candidate.UserID, candidate.LastActivityAt, claimedAt)
	return err
}

func (r *userInactivityCleanupRepository) ListDueInactiveUsers(ctx context.Context, now time.Time, limit int) ([]service.InactiveUserCandidate, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database is unavailable")
	}
	if limit <= 0 {
		limit = 200
	}
	query := fmt.Sprintf(`
SELECT u.id, u.email, u.username, %s AS last_activity_at
FROM user_inactivity_cleanups c
JOIN users u ON u.id = c.user_id
WHERE c.reminder_sent_at IS NOT NULL
  AND c.reminder_status = 'sent'
  AND c.reminder_sent_at <= ($1::timestamptz - INTERVAL '72 hours')
  AND c.delete_after <= $1
  AND u.deleted_at IS NULL
  AND u.role <> 'admin'
  AND u.status = 'active'
  AND %s < ($1 - INTERVAL '30 days')
  AND %s <= c.activity_at
ORDER BY c.delete_after ASC, u.id ASC
LIMIT $2`, userInactivityActivitySQL, userInactivityActivitySQL, userInactivityActivitySQL)
	return r.queryCandidates(ctx, query, now, limit)
}

func (r *userInactivityCleanupRepository) DeleteState(ctx context.Context, userID int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM user_inactivity_cleanups WHERE user_id = $1`, userID)
	return err
}

func (r *userInactivityCleanupRepository) queryCandidates(ctx context.Context, query string, args ...any) ([]service.InactiveUserCandidate, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.InactiveUserCandidate
	for rows.Next() {
		var candidate service.InactiveUserCandidate
		if err := rows.Scan(&candidate.UserID, &candidate.Email, &candidate.Username, &candidate.LastActivityAt); err != nil {
			return nil, err
		}
		candidate.LastActivityAt = candidate.LastActivityAt.UTC()
		out = append(out, candidate)
	}
	return out, rows.Err()
}
