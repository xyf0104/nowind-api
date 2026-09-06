package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type boundCodexSnapshotTestRepo struct {
	AccountRepository
	write func(context.Context, *Account, map[string]any) (bool, error)
}

func (r *boundCodexSnapshotTestRepo) UpdateOpenAICodexSnapshotIfIdentityMatches(ctx context.Context, account *Account, updates map[string]any) (bool, error) {
	return r.write(ctx, account, updates)
}

type legacyOnlyCodexSnapshotTestRepo struct {
	AccountRepository
	writes int
}

func (r *legacyOnlyCodexSnapshotTestRepo) UpdateOpenAICodexSnapshot(context.Context, int64, map[string]any) (bool, error) {
	r.writes++
	return true, nil
}

func (r *legacyOnlyCodexSnapshotTestRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	r.writes++
	return nil
}

func TestBoundCodexSnapshotGatewayCapturesImmutableIdentityBeforeGoroutine(t *testing.T) {
	proxyID := int64(9)
	account := &Account{
		ID: 88001, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &proxyID,
		Credentials: map[string]any{
			"access_token": "synthetic-old", "_token_version": int64(9007199254740993),
			"nested": map[string]any{"values": []any{"old", map[string]any{"key": "old"}}},
		},
		Extra: map[string]any{OpenAIWeeklyStateRevisionKey: int64(9007199254740991), "unrelated": true},
	}
	started, release := make(chan struct{}), make(chan struct{})
	finished := make(chan struct{})
	var captured *Account
	var patch map[string]any
	var contextErr error
	repo := &boundCodexSnapshotTestRepo{write: func(ctx context.Context, expected *Account, updates map[string]any) (bool, error) {
		close(started)
		<-release
		captured, patch, contextErr = expected, updates, ctx.Err()
		close(finished)
		return true, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	percent := 13.0
	snapshot := &OpenAICodexUsageSnapshot{PrimaryUsedPercent: &percent, PrimaryWindowMinutes: ptrIntWS(10080)}
	svc := &OpenAIGatewayService{accountRepo: repo, codexSnapshotThrottle: newAccountWriteThrottle(time.Minute)}
	svc.updateCodexUsageSnapshot(ctx, account, snapshot)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot writer did not start")
	}
	account.ID, account.Platform, account.Type = 88002, PlatformGemini, AccountTypeAPIKey
	account.Credentials["access_token"] = "synthetic-new"
	account.Credentials["_token_version"] = int64(9007199254740994)
	account.Extra[OpenAIWeeklyStateRevisionKey] = 1
	nested, ok := account.Credentials["nested"].(map[string]any)
	require.True(t, ok)
	values, ok := nested["values"].([]any)
	require.True(t, ok)
	values[0] = "new"
	nestedValue, ok := values[1].(map[string]any)
	require.True(t, ok)
	nestedValue["key"] = "new"
	proxyID = 10
	account.ParentAccountID = &proxyID
	account.QuotaDimension = QuotaDimensionSpark
	percent = 99
	close(release)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot writer did not finish")
	}
	require.NoError(t, contextErr, "persistence survives request cancellation")
	require.Equal(t, int64(88001), captured.ID)
	require.Equal(t, PlatformOpenAI, captured.Platform)
	require.Equal(t, AccountTypeOAuth, captured.Type)
	require.Equal(t, int64(9), *captured.ProxyID)
	require.Nil(t, captured.ParentAccountID)
	require.Equal(t, QuotaDimensionGlobal, captured.QuotaDimensionOrDefault())
	require.Equal(t, "synthetic-old", captured.Credentials["access_token"])
	require.Equal(t, json.Number("9007199254740993"), captured.Credentials["_token_version"])
	require.Equal(t, map[string]any{OpenAIWeeklyStateRevisionKey: json.Number("9007199254740991")}, captured.Extra)
	require.NotContains(t, patch, OpenAIWeeklyStateRevisionKey)
	capturedNested, ok := captured.Credentials["nested"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{"old", map[string]any{"key": "old"}}, capturedNested["values"])
	require.Equal(t, 13.0, patch["codex_7d_used_percent"])
}

func TestBoundCodexSnapshotUnknownOptionalRepositoryFailsClosed(t *testing.T) {
	account := &Account{ID: 88003, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	patch := map[string]any{"codex_usage_updated_at": time.Now().UTC().Format(time.RFC3339Nano)}
	legacy := &legacyOnlyCodexSnapshotTestRepo{}
	for _, repo := range []AccountRepository{nil, &stubOpenAIAccountRepo{}, legacy} {
		applied, err := persistOrderedOpenAICodexSnapshot(context.Background(), repo, account, patch)
		require.ErrorContains(t, err, "identity-bound ordered")
		require.False(t, applied)
	}
	_, err := (&AccountUsageService{accountRepo: legacy}).persistOpenAICodexProbeSnapshot(account, patch)
	require.ErrorContains(t, err, "identity-bound ordered")
	require.Zero(t, legacy.writes, "neither legacy ordered writes nor UpdateExtra are a fallback")
}

func TestBoundCodexSnapshotSparkNeverUsesGlobalHeadersOrUncapturedParent(t *testing.T) {
	parentID := int64(88004)
	shadow := &Account{ID: 88005, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID, QuotaDimension: QuotaDimensionSpark}
	writes := 0
	repo := &boundCodexSnapshotTestRepo{write: func(context.Context, *Account, map[string]any) (bool, error) {
		writes++
		return true, nil
	}}
	headers := http.Header{"X-Codex-Primary-Used-Percent": []string{"50"}}
	gateway := &OpenAIGatewayService{accountRepo: repo}
	gateway.UpdateCodexUsageSnapshotFromHeaders(context.Background(), shadow, headers)
	gateway.updateCodexUsageSnapshot(context.Background(), shadow, ParseCodexRateLimitHeaders(headers))
	(&RateLimitService{accountRepo: repo}).persistOpenAICodexSnapshot(context.Background(), shadow, headers)
	for _, repo := range []AccountRepository{repo, &legacyOnlyCodexSnapshotTestRepo{}} {
		applied, err := (&AccountUsageService{accountRepo: repo}).persistOpenAICodexProbeSnapshot(shadow,
			map[string]any{"codex_usage_updated_at": time.Now().UTC().Format(time.RFC3339Nano), "codex_7d_used_percent": 20.0})
		require.False(t, applied)
		require.ErrorContains(t, err, "did not capture the parent request credentials/proxy")
	}
	require.Zero(t, writes)
}

func TestBoundCodexSnapshotCloneRejectsInvalidCredentialsWithoutLeakingValues(t *testing.T) {
	_, err := cloneOpenAICodexSnapshotIdentity(&Account{Credentials: map[string]any{"synthetic-secret": make(chan int)}})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "synthetic-secret")
}

func TestBoundCodexSnapshotClonePreservesRevisionPresence(t *testing.T) {
	for _, extra := range []map[string]any{nil, {"unrelated": make(chan int)}, {OpenAIWeeklyStateRevisionKey: nil}} {
		cloned, err := cloneOpenAICodexSnapshotIdentity(&Account{Extra: extra})
		require.NoError(t, err)
		_, wantPresent := extra[OpenAIWeeklyStateRevisionKey]
		_, gotPresent := cloned.Extra[OpenAIWeeklyStateRevisionKey]
		require.Equal(t, wantPresent, gotPresent)
		require.NotContains(t, cloned.Extra, "unrelated")
	}
	_, err := cloneOpenAICodexSnapshotIdentity(&Account{Extra: map[string]any{OpenAIWeeklyStateRevisionKey: make(chan int)}})
	require.EqualError(t, err, "codex snapshot revision is not JSON-encodable")
}
