//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestInactiveUserCleanupRepository_LifecycleIntegration(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	activity := now.Add(-30 * 24 * time.Hour)
	u := mustCreateUser(t, testEntClient(t), &service.User{CreatedAt: activity})
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM users WHERE id = $1`, u.ID) })
	repo := NewInactiveUserCleanupRepository(integrationDB)
	candidate := service.InactiveUserCandidate{UserID: u.ID, Email: u.Email, LastActivityAt: activity}

	// Exactly 30 days is not enough; the threshold is strictly greater than 30d.
	claimed, err := repo.ClaimReminder(ctx, candidate, now, now.Add(72*time.Hour))
	require.NoError(t, err)
	require.False(t, claimed)
	now = now.Add(time.Microsecond)
	claimed, err = repo.ClaimReminder(ctx, candidate, now, now.Add(72*time.Hour))
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = repo.ClaimReminder(ctx, candidate, now.Add(time.Minute), now.Add(72*time.Hour))
	require.NoError(t, err)
	require.False(t, claimed, "another worker cannot steal an unexpired claim")

	// An expired worker cannot finalize or release the replacement claim.
	retryAt := now.Add(service.InactiveUserCleanupClaimLease + time.Microsecond)
	claimed, err = repo.ClaimReminder(ctx, candidate, retryAt, retryAt.Add(72*time.Hour))
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, repo.MarkReminderSent(ctx, candidate, now, now, now.Add(72*time.Hour)))
	require.NoError(t, repo.ReleaseReminderClaim(ctx, candidate, now))
	var status string
	var claimedAt time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT reminder_status, reminder_claimed_at FROM user_inactivity_cleanups WHERE user_id = $1`, u.ID).
		Scan(&status, &claimedAt))
	require.Equal(t, "sending", status)
	require.True(t, retryAt.Equal(claimedAt))

	sentAt := retryAt.Add(45 * time.Second)
	deadline := sentAt.Add(72 * time.Hour)
	require.NoError(t, repo.MarkReminderSent(ctx, candidate, retryAt, sentAt, deadline))
	var storedSent, storedDeadline time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT reminder_sent_at, delete_after FROM user_inactivity_cleanups WHERE user_id = $1`, u.ID).
		Scan(&storedSent, &storedDeadline))
	require.True(t, sentAt.Equal(storedSent))
	require.True(t, deadline.Equal(storedDeadline))
	assertDue := func(at time.Time, expected bool) {
		t.Helper()
		due, err := repo.ListDueInactiveUsers(ctx, at, 10000)
		require.NoError(t, err)
		found := false
		for _, item := range due {
			found = found || item.UserID == u.ID
		}
		require.Equal(t, expected, found)
	}
	assertDue(deadline.Add(-time.Microsecond), false)
	assertDue(deadline, true)
	claimed, err = repo.ClaimReminder(ctx, candidate, deadline, deadline.Add(72*time.Hour))
	require.NoError(t, err)
	require.False(t, claimed, "a sent reminder cannot be claimed again")

	// Even a legacy prematurely calculated delete_after cannot shorten grace.
	_, err = integrationDB.ExecContext(ctx, `UPDATE user_inactivity_cleanups SET delete_after = $2 WHERE user_id = $1`, u.ID, retryAt)
	require.NoError(t, err)
	assertDue(deadline.Add(-time.Microsecond), false)
	assertDue(deadline, true)

	// Login cancels the cycle, including when it predates the rolling cutoff
	// after a prolonged worker outage. A stale candidate must not be reused.
	loginAt := sentAt.Add(time.Hour)
	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET last_login_at = $2 WHERE id = $1`, u.ID, loginAt)
	require.NoError(t, err)
	assertDue(loginAt.Add(40*24*time.Hour), false)
	_, err = repo.CancelReactivated(ctx, loginAt.Add(10*24*time.Hour))
	require.NoError(t, err)
	var states int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_inactivity_cleanups WHERE user_id = $1`, u.ID).Scan(&states))
	require.Zero(t, states)
	newCycleAt := loginAt.Add(31 * 24 * time.Hour)
	claimed, err = repo.ClaimReminder(ctx, candidate, newCycleAt, newCycleAt.Add(72*time.Hour))
	require.NoError(t, err)
	require.False(t, claimed)
	candidate.LastActivityAt = loginAt
	claimed, err = repo.ClaimReminder(ctx, candidate, newCycleAt, newCycleAt.Add(72*time.Hour))
	require.NoError(t, err)
	require.True(t, claimed, "a genuinely new inactivity cycle can be warned")
}
