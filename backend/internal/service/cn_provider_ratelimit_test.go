//go:build unit

package service

import (
	"context"
	"errors"
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

func TestIsCNProviderConcurrencyLimit403_RequiresExactKimiMessage(t *testing.T) {
	kimi := &Account{Platform: PlatformKimi}

	require.True(t, isCNProviderConcurrencyLimit403(kimi, kimiConcurrentRequestLimitMessage))
	require.True(t, isCNProviderConcurrencyLimit403(kimi, "  "+kimiConcurrentRequestLimitMessage+"\n"))

	for name, tc := range map[string]struct {
		account *Account
		message string
	}{
		"generic concurrency wording":    {kimi, "concurrent request limit reached"},
		"near match missing punctuation": {kimi, "You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again"},
		"other CN provider":              {&Account{Platform: PlatformZhipu}, kimiConcurrentRequestLimitMessage},
		"non CN provider":                {&Account{Platform: PlatformOpenAI}, kimiConcurrentRequestLimitMessage},
		"nil account":                    {nil, kimiConcurrentRequestLimitMessage},
	} {
		t.Run(name, func(t *testing.T) {
			require.False(t, isCNProviderConcurrencyLimit403(tc.account, tc.message))
		})
	}
}

func TestHandle403_OtherCNProviderWithKimiConcurrencyMessageKeepsNormalPolicy(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{openAI403DisableThreshold}}
	blocker := &runtimeBlockRecorder{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc.SetOpenAI403CounterCache(counter)
	svc.SetAccountRuntimeBlocker(blocker)
	account := &Account{ID: 506, Platform: PlatformZhipu, Type: AccountTypeAPIKey}

	shouldDisable := svc.HandleUpstreamError(
		context.Background(), account, http.StatusForbidden, http.Header{},
		[]byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`),
	)

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls, "only Kimi receives the transient concurrency exception")
	require.Zero(t, repo.tempCalls)
	require.Empty(t, counter.counts, "normal 403 policy must consume the counter")
	require.Equal(t, []string{"auth_error"}, blocker.reasons)
}

func TestHandle403_KimiConcurrencyLimitUsesTemporaryCooldown(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{openAI403DisableThreshold}}
	blocker := &runtimeBlockRecorder{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc.SetOpenAI403CounterCache(counter)
	svc.SetAccountRuntimeBlocker(blocker)
	account := &Account{ID: 507, Platform: PlatformKimi, Type: AccountTypeAPIKey}

	shouldDisable := svc.HandleUpstreamError(
		context.Background(), account, http.StatusForbidden, http.Header{},
		[]byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`),
	)

	require.True(t, shouldDisable, "the current request must fail over")
	require.Zero(t, repo.setErrorCalls)
	require.Equal(t, 1, repo.tempCalls)
	require.Contains(t, repo.lastTempReason, cnConcurrencyLimitReasonPrefix)
	require.Equal(t, []int64{openAI403DisableThreshold}, counter.counts, "the exact transient error must bypass the permanent-error counter")
	require.Len(t, blocker.accounts, 1)
	require.Equal(t, cnConcurrencyLimitReasonPrefix, blocker.reasons[0])
	require.True(t, blocker.until[0].After(time.Now()))
}

func TestHandle403_KimiConcurrencyLimitRepositoryFailureKeepsRuntimeBlock(t *testing.T) {
	repo := &rateLimitAccountRepoStub{tempErr: errors.New("repository unavailable")}
	counter := &openAI403CounterCacheStub{counts: []int64{openAI403DisableThreshold}}
	blocker := &runtimeBlockRecorder{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc.SetOpenAI403CounterCache(counter)
	svc.SetAccountRuntimeBlocker(blocker)
	account := &Account{ID: 508, Platform: PlatformKimi, Type: AccountTypeAPIKey}

	shouldDisable := svc.HandleUpstreamError(
		context.Background(), account, http.StatusForbidden, http.Header{},
		[]byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`),
	)

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.tempCalls)
	require.Zero(t, repo.setErrorCalls, "a failed cooldown write must not turn this into a permanent error")
	require.Equal(t, []int64{openAI403DisableThreshold}, counter.counts)
	require.Len(t, blocker.accounts, 1, "the in-memory block protects immediately even if persistence is unavailable")
	require.Same(t, account, blocker.accounts[0])
	require.Equal(t, cnConcurrencyLimitReasonPrefix, blocker.reasons[0])
	require.True(t, blocker.until[0].After(time.Now()))
}

func TestHandle403_KimiConcurrencyNearMatchKeepsNormalPermanentErrorPolicy(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{openAI403DisableThreshold}}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc.SetOpenAI403CounterCache(counter)
	account := &Account{ID: 509, Platform: PlatformKimi, Type: AccountTypeAPIKey}

	shouldDisable := svc.HandleUpstreamError(
		context.Background(), account, http.StatusForbidden, http.Header{},
		[]byte(`{"error":{"message":"You've reached your concurrent request limit. Please contact support."}}`),
	)

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls, "only the known exact transient response receives the cooldown exception")
	require.Zero(t, repo.tempCalls)
}
