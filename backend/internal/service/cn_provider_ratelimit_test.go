//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type cnRateLimitAccountRepoStub struct {
	rateLimitAccountRepoStub
	rateLimitedCalls int
	rateLimitedID    int64
	rateLimitedUntil time.Time
}

func (r *cnRateLimitAccountRepoStub) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedCalls++
	r.rateLimitedID = id
	r.rateLimitedUntil = resetAt
	return nil
}

func TestHandleUpstreamError_CNProvider402UsesRecoverableBalancePause(t *testing.T) {
	repo := &cnRateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:          501,
		Platform:    PlatformDeepseek,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"account_mode": AccountModePayG},
	}

	shouldDisable := svc.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusPaymentRequired,
		http.Header{},
		[]byte(`{"error":{"message":"insufficient balance"}}`),
	)

	require.True(t, shouldDisable)
	require.Zero(t, repo.setErrorCalls, "recoverable balance exhaustion must not permanently error the account")
	require.Equal(t, 1, repo.tempCalls)
	require.Contains(t, repo.lastTempReason, cnBalanceLowReasonPrefix)
	require.Equal(t, true, repo.lastExtraUpdates[cnExtraKey(PlatformDeepseek, cnBalanceExtraSuffixLow)])
}

func TestHandleUpstreamError_CNProvider429InsufficientBalanceUsesRecoverablePause(t *testing.T) {
	repo := &cnRateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:          502,
		Platform:    PlatformZhipu,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"account_mode": AccountModePayG},
	}

	shouldDisable := svc.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusTooManyRequests,
		http.Header{},
		[]byte(`{"error":{"message":"余额不足"}}`),
	)

	require.False(t, shouldDisable, "429 keeps the existing failover contract while persisting a recoverable pause")
	require.Zero(t, repo.setErrorCalls)
	require.Equal(t, 1, repo.tempCalls)
	require.Contains(t, repo.lastTempReason, cnBalanceLowReasonPrefix)
}

func TestHandleUpstreamError_CNCodingPlan429UsesSnapshotReset(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fiveHourReset := now.Add(2 * time.Hour)
	weeklyReset := now.Add(5 * 24 * time.Hour)
	repo := &cnRateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:          503,
		Platform:    PlatformKimi,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"account_mode": AccountModeCoding},
		Extra: map[string]any{
			cnExtraKey(PlatformKimi, cnExtraSuffix5hReset):     fiveHourReset.Format(time.RFC3339),
			cnExtraKey(PlatformKimi, cnExtraSuffixWeeklyReset): weeklyReset.Format(time.RFC3339),
		},
	}

	shouldDisable := svc.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusTooManyRequests,
		http.Header{},
		[]byte(`{"error":{"message":"quota exhausted"}}`),
	)

	require.False(t, shouldDisable)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, account.ID, repo.rateLimitedID)
	require.True(t, fiveHourReset.Equal(repo.rateLimitedUntil), "coding-plan cooldown should use the earliest future quota reset")
}

func TestHandle403_CNProviderHTMLBodySkipsAccountPenalty(t *testing.T) {
	for _, platform := range []string{PlatformKimi, PlatformZhipu, PlatformDeepseek} {
		repo := &rateLimitAccountRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		account := &Account{ID: 504, Platform: platform, Type: AccountTypeAPIKey}

		shouldDisable := svc.HandleUpstreamError(
			context.Background(),
			account,
			http.StatusForbidden,
			http.Header{},
			[]byte("<html><body>Access denied by CDN</body></html>"),
		)

		require.False(t, shouldDisable, "%s: CDN/proxy HTML must not become account-auth evidence", platform)
		require.Zero(t, repo.setErrorCalls)
		require.Zero(t, repo.tempCalls)
	}
}

func TestHandle403_CNProviderStructured403UsesOpenAICooldownPolicy(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{1}}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc.SetOpenAI403CounterCache(counter)
	account := &Account{ID: 505, Platform: PlatformKimi, Type: AccountTypeAPIKey}

	shouldDisable := svc.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"error":{"message":"forbidden"}}`),
	)

	require.True(t, shouldDisable)
	require.Zero(t, repo.setErrorCalls)
	require.Equal(t, 1, repo.tempCalls)
	require.Contains(t, repo.lastTempReason, "(1/3)")
}
