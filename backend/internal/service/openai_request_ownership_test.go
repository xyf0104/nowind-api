package service

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// All credentials, states, and WebSocket connections in this file are test-only.
func isolationAuditAccount(mode codexFingerprintMode) *Account {
	return &Account{
		ID:          5001,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "audit-account"},
		Extra: prepareCodexFingerprintExtraForCreate(PlatformOpenAI, AccountTypeOAuth,
			map[string]any{codexFingerprintModeExtraKey: string(mode)}),
	}
}

func isolationAuditHeaders(t *testing.T, svc *OpenAIGatewayService, c *gin.Context, account *Account, turnState string) http.Header {
	t.Helper()
	stageCodexFingerprintIDs(c, resolveCodexFingerprintIDsFromRequest(account, c.Request.Header))
	headers, _, err := svc.buildOpenAIWSHeaders(context.Background(), c, account, "audit-token",
		OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2},
		false, turnState, "", "")
	require.NoError(t, err)
	return headers
}

type isolationAuditDialer struct{}

func (*isolationAuditDialer) Dial(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) {
	responseHeaders := make(http.Header)
	responseHeaders.Set(openAIWSTurnStateHeader, "opaque-first-handshake-state")
	return &openAIWSFakeConn{}, http.StatusSwitchingProtocols, responseHeaders, nil
}

func TestOpenAIRequestOwnershipPoolCustomerBoundary(t *testing.T) {
	for _, mode := range []codexFingerprintMode{codexFingerprintOff, codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull} {
		for _, sameRawSession := range []bool{false, true} {
			name := string(mode) + "/distinct-client-sessions"
			if sameRawSession {
				name = string(mode) + "/same-client-session-different-api-keys"
			}
			t.Run(name, func(t *testing.T) {
				cfg := &config.Config{}
				cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
				cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
				cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
				pool := newOpenAIWSConnPool(cfg)
				t.Cleanup(pool.Close)
				pool.setClientDialerForTest(&isolationAuditDialer{})
				account := isolationAuditAccount(mode)
				svc := &OpenAIGatewayService{}
				firstContext, _ := newTurnStateTestContext(t, 101, "conversation-a")
				secondSession := "conversation-b"
				if sameRawSession {
					secondSession = "conversation-a"
				}
				secondContext, _ := newTurnStateTestContext(t, 202, secondSession)
				firstRequest := openAIWSAcquireRequest{Account: account, WSURL: "wss://audit.invalid/v1/responses",
					Headers: isolationAuditHeaders(t, svc, firstContext, account, ""), Owner: openAIWSOwnershipForRequest(firstContext, account)}
				secondRequest := openAIWSAcquireRequest{Account: account, WSURL: firstRequest.WSURL,
					Headers: isolationAuditHeaders(t, svc, secondContext, account, ""), Owner: openAIWSOwnershipForRequest(secondContext, account)}
				first, err := pool.Acquire(context.Background(), firstRequest)
				require.NoError(t, err)
				firstID := first.ConnID()
				first.Release()
				second, err := pool.Acquire(context.Background(), secondRequest)
				require.NoError(t, err)
				defer second.Release()
				if second.Reused() {
					t.Logf("second customer inherited first connection %s and handshake state %q", second.ConnID(), second.HandshakeHeader(openAIWSTurnStateHeader))
				}
				assert.NotEqual(t, firstID, second.ConnID(), "different authenticated customers must not share a stateful handshake")
				assert.False(t, sameOpenAIWSPrewarmTarget(firstRequest, secondRequest), "prewarm targets must also retain customer isolation")
			})
		}
	}
}

func TestOpenAIRequestOwnershipCapturedBeforeFingerprintRewrite(t *testing.T) {
	for _, mode := range []codexFingerprintMode{codexFingerprintSession, codexFingerprintFull} {
		for _, headerPresent := range []bool{false, true} {
			name := string(mode) + "/body-only-session"
			if headerPresent {
				name = string(mode) + "/header-and-body-session"
			}
			t.Run(name, func(t *testing.T) {
				account := isolationAuditAccount(mode)
				build := func(session string) (openAIWSOwnership, *codexFingerprintIDs, map[string]any) {
					c, _ := newTurnStateTestContext(t, 101, "")
					headers := c.Request.Header
					if headerPresent {
						headers.Set("session-id", session)
					}
					ids := resolveCodexFingerprintIDsFromRequest(account, headers)
					require.NotNil(t, ids)
					body := map[string]any{"prompt_cache_key": session, "client_metadata": map[string]any{"session_id": session}}
					captureOpenAIRequestOwnership(c, payloadAsJSONBytes(body))
					before := openAIWSOwnershipForRequest(c, account)
					require.True(t, applyCodexFingerprintClientMetadata(body, ids))
					applyCodexFingerprintHeaders(headers, ids)
					require.Equal(t, before, openAIWSOwnershipForRequest(c, account))
					return before, ids, body
				}
				firstOwner, firstIDs, firstBody := build("conversation-a")
				secondOwner, secondIDs, secondBody := build("conversation-b")
				assert.Equal(t, firstIDs.installationID, secondIDs.installationID, "the account device remains stable")
				assert.NotEqual(t, firstOwner, secondOwner, "internal ownership must remain distinct")
				assert.Equal(t, firstIDs.sessionID, secondIDs.sessionID, "outgoing fingerprint semantics are unchanged")
				assert.Equal(t, firstBody["prompt_cache_key"], secondBody["prompt_cache_key"], "this fix does not change outgoing cache policy")
			})
		}
	}
}

func TestOpenAIRequestOwnershipStateStoreCustomerBoundary(t *testing.T) {
	svc := &OpenAIGatewayService{}
	first, _ := newTurnStateTestContext(t, 101, "same-client-session")
	second, _ := newTurnStateTestContext(t, 202, "same-client-session")
	firstHash := svc.GenerateSessionHash(first, nil)
	secondHash := svc.GenerateSessionHash(second, nil)
	require.NotEmpty(t, firstHash)
	backing := NewOpenAIWSStateStore(nil)
	account := isolationAuditAccount(codexFingerprintOff)
	stateStore := &ownedOpenAIWSStateStore{backing, openAIWSOwnershipForRequest(first, account)}
	stateStore.BindSessionTurnState(9, firstHash, "opaque-customer-a-state", time.Minute)
	stateStore.BindSessionConn(9, firstHash, "customer-a-connection", time.Minute)
	secondStore := &ownedOpenAIWSStateStore{backing, openAIWSOwnershipForRequest(second, account)}
	state, stateFound := secondStore.GetSessionTurnState(9, secondHash)
	conn, connFound := secondStore.GetSessionConn(9, secondHash)
	assert.False(t, stateFound, "customer B retrieved customer A turn state: %s", state)
	assert.False(t, connFound, "customer B retrieved customer A connection binding: %s", conn)
}

func TestOpenAIRequestOwnershipWSGuardAccountBoundary(t *testing.T) {
	svc := &OpenAIGatewayService{}
	c, _ := newTurnStateTestContext(t, 101, "conversation-a")
	first := isolationAuditAccount(codexFingerprintOff)
	upstream := make(http.Header)
	upstream.Set(openAIWSTurnStateHeader, "opaque-account-a-state")
	svc.relayOpenAICodexTurnState(c, first, upstream)
	second := isolationAuditAccount(codexFingerprintOff)
	second.ID++
	headers := isolationAuditHeaders(t, svc, c, second, "opaque-account-a-state")
	assert.Empty(t, headers.Get(openAIWSTurnStateHeader), "WS must not replay state known to belong to a different upstream account")
	httpControl := upstream.Clone()
	svc.guardOpenAICodexTurnStateEcho(c, second, httpControl)
	require.Empty(t, httpControl.Get(openAIWSTurnStateHeader), "the existing HTTP guard recognizes this account mismatch")
}

func TestOpenAIRequestOwnershipTurnStateBlobBoundary(t *testing.T) {
	svc := &OpenAIGatewayService{}
	c, _ := newTurnStateTestContext(t, 101, "conversation-a")
	first := &Account{ID: 5001}
	second := &Account{ID: 5002}
	upstreamA := make(http.Header)
	upstreamA.Set(openAIWSTurnStateHeader, "opaque-account-a-state")
	svc.relayOpenAICodexTurnState(c, first, upstreamA)
	upstreamB := make(http.Header)
	upstreamB.Set(openAIWSTurnStateHeader, "opaque-account-b-state")
	svc.relayOpenAICodexTurnState(c, second, upstreamB)
	lateEcho := upstreamA.Clone()
	svc.guardOpenAICodexTurnStateEcho(c, second, lateEcho)
	assert.Empty(t, lateEcho.Get(openAIWSTurnStateHeader), "a later commit on B must not relabel an older A blob as B-owned")
}

func TestOpenAIRequestOwnershipPoolConversationBoundary(t *testing.T) {
	for _, mode := range []codexFingerprintMode{codexFingerprintOff, codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
			pool := newOpenAIWSConnPool(cfg)
			t.Cleanup(pool.Close)
			pool.setClientDialerForTest(&isolationAuditDialer{})
			account := isolationAuditAccount(mode)
			svc := &OpenAIGatewayService{}
			firstContext, _ := newTurnStateTestContext(t, 101, "conversation-a")
			secondContext, _ := newTurnStateTestContext(t, 101, "conversation-b")
			request := func(c *gin.Context) openAIWSAcquireRequest {
				return openAIWSAcquireRequest{Account: account, WSURL: "wss://audit.invalid/v1/responses",
					Headers: isolationAuditHeaders(t, svc, c, account, ""), Owner: openAIWSOwnershipForRequest(c, account)}
			}
			first, err := pool.Acquire(context.Background(), request(firstContext))
			require.NoError(t, err)
			first.Release()
			second, err := pool.Acquire(context.Background(), request(secondContext))
			require.NoError(t, err)
			assert.NotEqual(t, first.ConnID(), second.ConnID())
			second.Release()
			repeat, err := pool.Acquire(context.Background(), request(firstContext))
			require.NoError(t, err)
			assert.Equal(t, first.ConnID(), repeat.ConnID(), "same-owner reuse remains available")
			repeat.Release()
		})
	}
}

func TestOpenAIRequestOwnershipPinnedChainSurvivesTokenRefreshAndMissingSession(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	pool := newOpenAIWSConnPool(cfg)
	t.Cleanup(pool.Close)
	pool.setClientDialerForTest(&isolationAuditDialer{})
	svc := &OpenAIGatewayService{}
	account := isolationAuditAccount(codexFingerprintOff)
	firstContext, _ := newTurnStateTestContext(t, 101, "conversation-a")
	firstOwner := openAIWSOwnershipForRequest(firstContext, account)
	req := openAIWSAcquireRequest{Account: account, WSURL: "wss://audit.invalid/v1/responses", Owner: firstOwner}
	first, err := pool.Acquire(context.Background(), req)
	require.NoError(t, err)
	first.Release()
	backing := NewOpenAIWSStateStore(nil)
	firstStore := &ownedOpenAIWSStateStore{backing, firstOwner}
	firstStore.BindResponseConn("resp-chain", first.ConnID(), time.Hour)

	account.Credentials["access_token"] = "rotated-token"
	secondContext, _ := newTurnStateTestContext(t, 101, "")
	secondOwner := openAIWSOwnershipForRequest(secondContext, account)
	require.NotEqual(t, firstOwner.conversation, secondOwner.conversation)
	require.Equal(t, firstOwner.upstream, secondOwner.upstream)
	secondStore := &ownedOpenAIWSStateStore{backing, secondOwner}
	connID, ok := secondStore.GetResponseConn("resp-chain")
	require.True(t, ok)
	req.Owner, req.PreferredConnID = secondOwner, connID
	req.Continuation, req.ForcePreferredConn = true, true
	second, err := pool.Acquire(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, first.ConnID(), second.ConnID())
	second.Release()

	otherClient, _ := newTurnStateTestContext(t, 202, "conversation-a")
	otherOwner := openAIWSOwnershipForRequest(otherClient, account)
	_, ok = (&ownedOpenAIWSStateStore{backing, otherOwner}).GetResponseConn("resp-chain")
	require.False(t, ok)
	req.Owner = otherOwner
	_, err = pool.Acquire(context.Background(), req)
	require.ErrorIs(t, err, errOpenAIWSPreferredConnUnavailable, "even a supplied preferred ID cannot cross customers")

	req.Owner = secondOwner
	req.ProxyURL = "http://different-proxy.invalid"
	_, err = pool.Acquire(context.Background(), req)
	require.ErrorIs(t, err, errOpenAIWSPreferredConnUnavailable)
	req.ProxyURL = ""
	account.Credentials["chatgpt_account_id"] = "different-upstream-principal"
	req.Owner = openAIWSOwnershipForRequest(secondContext, account)
	_, err = pool.Acquire(context.Background(), req)
	require.ErrorIs(t, err, errOpenAIWSPreferredConnUnavailable)

	stateHeader := make(http.Header)
	stateHeader.Set(openAIWSTurnStateHeader, "opaque-refresh-state")
	account.Credentials["chatgpt_account_id"] = "audit-account"
	svc.noteOpenAICodexTurnStateProvenance(firstContext, account, "opaque-refresh-state")
	account.Credentials["access_token"] = "rotated-token-again"
	svc.guardOpenAICodexTurnStateEcho(firstContext, account, stateHeader)
	require.NotEmpty(t, stateHeader.Get(openAIWSTurnStateHeader))
}

func TestOpenAIRequestOwnershipUnownedPoolRequestsCannotReuse(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
	pool := newOpenAIWSConnPool(cfg)
	t.Cleanup(pool.Close)
	pool.setClientDialerForTest(&isolationAuditDialer{})
	req := openAIWSAcquireRequest{Account: isolationAuditAccount(codexFingerprintOff), WSURL: "wss://audit.invalid/v1/responses"}
	first, err := pool.Acquire(context.Background(), req)
	require.NoError(t, err)
	first.Release()
	req.PreferredConnID = first.ConnID()
	req.Continuation = true
	second, err := pool.Acquire(context.Background(), req)
	require.NoError(t, err)
	require.NotEqual(t, first.ConnID(), second.ConnID())
	second.Release()
}

func TestOpenAIRequestOwnershipStateStoreUpstreamAndConversationBoundary(t *testing.T) {
	backing := NewOpenAIWSStateStore(nil)
	account := isolationAuditAccount(codexFingerprintOff)
	c, _ := newTurnStateTestContext(t, 101, "conversation-a")
	owner := openAIWSOwnershipForRequest(c, account)
	store := &ownedOpenAIWSStateStore{backing, owner}
	store.BindSessionTurnState(9, "legacy-hash", "opaque-a", time.Minute)
	store.BindSessionConn(9, "legacy-hash", "conn-a", time.Minute)
	store.BindResponseConn("resp-a", "conn-a", time.Hour)
	for _, variant := range []string{"conversation", "account", "principal", "proxy"} {
		t.Run(variant, func(t *testing.T) {
			next := isolationAuditAccount(codexFingerprintOff)
			nextContext, _ := newTurnStateTestContext(t, 101, "conversation-a")
			switch variant {
			case "conversation":
				nextContext.Request.Header.Set("session_id", "conversation-b")
			case "account":
				next.ID++
			case "principal":
				next.Credentials["chatgpt_account_id"] = "different-principal"
			case "proxy":
				proxyID := int64(18)
				next.ProxyID = &proxyID
			}
			other := &ownedOpenAIWSStateStore{backing, openAIWSOwnershipForRequest(nextContext, next)}
			_, ok := other.GetSessionTurnState(9, "legacy-hash")
			require.False(t, ok)
			_, ok = other.GetSessionConn(9, "legacy-hash")
			require.False(t, ok)
			other.DeleteSessionTurnState(9, "legacy-hash")
			value, ok := store.GetSessionTurnState(9, "different-derived-hash")
			require.True(t, ok)
			require.Equal(t, "opaque-a", value)
		})
	}
}

func TestOpenAIRequestOwnershipPrewarmDropsOldCustomer(t *testing.T) {
	for _, mode := range []codexFingerprintMode{codexFingerprintOff, codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			pool := newOpenAIWSConnPool(cfg)
			t.Cleanup(pool.Close)
			dialer := newOpenAIWSFirstDialBlockingCaptureDialer()
			pool.setClientDialerForTest(dialer)
			var once sync.Once
			release := func() { once.Do(func() { close(dialer.releaseFirst) }) }
			t.Cleanup(release)
			account := isolationAuditAccount(mode)
			svc := &OpenAIGatewayService{}
			first, _ := newTurnStateTestContext(t, 101, "same-raw-session")
			second, _ := newTurnStateTestContext(t, 202, "same-raw-session")
			oldReq := openAIWSAcquireRequest{Account: account, WSURL: "wss://audit.invalid/v1/responses",
				Headers: isolationAuditHeaders(t, svc, first, account, ""), Owner: openAIWSOwnershipForRequest(first, account)}
			newReq := openAIWSAcquireRequest{Account: account, WSURL: oldReq.WSURL,
				Headers: isolationAuditHeaders(t, svc, second, account, ""), Owner: openAIWSOwnershipForRequest(second, account)}
			ap := pool.getOrCreateAccountPool(account.ID)
			ap.mu.Lock()
			ap.lastAcquire = &oldReq
			ap.mu.Unlock()
			pool.ensureTargetIdleAsync(account.ID)
			select {
			case <-dialer.firstStarted:
			case <-time.After(time.Second):
				t.Fatal("prewarm did not start")
			}
			ap.mu.Lock()
			ap.lastAcquire = &newReq
			ap.mu.Unlock()
			release()
			require.Eventually(t, func() bool {
				ap.mu.Lock()
				defer ap.mu.Unlock()
				if ap.prewarmActive || len(ap.conns) != 1 {
					return false
				}
				for _, conn := range ap.conns {
					return conn.handshakeCompatibility.owner == newReq.Owner
				}
				return false
			}, 2*time.Second, 5*time.Millisecond)
			require.Equal(t, 2, dialer.DialCount())
		})
	}
}

func TestOpenAIRequestOwnershipTurnStateConcurrentExplicitGrants(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := isolationAuditAccount(codexFingerprintOff)
	contexts := make([]*gin.Context, 16)
	for i := range contexts {
		contexts[i], _ = newTurnStateTestContext(t, int64(i+1), "session")
		captureOpenAIRequestOwnership(contexts[i], nil)
	}
	var wg sync.WaitGroup
	for _, c := range contexts {
		wg.Add(1)
		go func(c *gin.Context) {
			defer wg.Done()
			for i := 0; i < 32; i++ {
				svc.noteOpenAICodexTurnStateProvenance(c, account, "shared-upstream-opaque-value")
			}
		}(c)
	}
	wg.Wait()
	for _, c := range contexts {
		h := make(http.Header)
		h.Set(openAIWSTurnStateHeader, "shared-upstream-opaque-value")
		svc.guardOpenAICodexTurnStateEcho(c, account, h)
		require.NotEmpty(t, h.Get(openAIWSTurnStateHeader))
	}
	foreign, _ := newTurnStateTestContext(t, 101, "session")
	h := make(http.Header)
	h.Set(openAIWSTurnStateHeader, "shared-upstream-opaque-value")
	svc.guardOpenAICodexTurnStateEcho(foreign, account, h)
	require.Empty(t, h.Get(openAIWSTurnStateHeader))
}

func TestOpenAIRequestOwnershipCapacityOneEvictsIdleForeignOwnerImmediately(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	pool := newOpenAIWSConnPool(cfg)
	t.Cleanup(pool.Close)
	dialer := &openAIWSCountingDialer{}
	pool.setClientDialerForTest(dialer)
	account := isolationAuditAccount(codexFingerprintOff)
	firstContext, _ := newTurnStateTestContext(t, 101, "conversation-a")
	secondContext, _ := newTurnStateTestContext(t, 202, "conversation-b")
	req := openAIWSAcquireRequest{Account: account, WSURL: "wss://audit.invalid/v1/responses", Owner: openAIWSOwnershipForRequest(firstContext, account)}
	first, err := pool.Acquire(context.Background(), req)
	require.NoError(t, err)
	first.Release()
	req.Owner = openAIWSOwnershipForRequest(secondContext, account)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	second, err := pool.Acquire(ctx, req)
	require.NoError(t, err, "must replace an idle incompatible owner without waiting for idle TTL")
	require.NotEqual(t, first.ConnID(), second.ConnID())
	require.Equal(t, 2, dialer.DialCount())
	second.Release()
}
