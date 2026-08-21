package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newTurnStateTestContext(t *testing.T, apiKeyID int64, sessionID string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if sessionID != "" {
		c.Request.Header.Set("session_id", sessionID)
	}
	if apiKeyID > 0 {
		c.Set("api_key", &APIKey{ID: apiKeyID})
	}
	return c, recorder
}

func TestOpenAICodexTurnStateSeed(t *testing.T) {
	c, _ := newTurnStateTestContext(t, 7, "sess-1")
	require.Equal(t, "7\x00sess-1", openAICodexTurnStateSeed(c))

	c.Request.Header.Set("session-id", "sess-hyphen")
	require.Equal(t, "7\x00sess-hyphen", openAICodexTurnStateSeed(c))

	cNoSession, _ := newTurnStateTestContext(t, 7, "")
	require.Empty(t, openAICodexTurnStateSeed(cNoSession))
	require.Empty(t, openAICodexTurnStateSeed(nil))
}

func TestRelayOpenAICodexTurnStateRecordsCommittedAccount(t *testing.T) {
	svc := &OpenAIGatewayService{}
	c, _ := newTurnStateTestContext(t, 7, "sess-relay")
	upstream := http.Header{}
	upstream.Set(openAIWSTurnStateHeader, "blob-A")

	svc.relayOpenAICodexTurnState(c, &Account{ID: 42}, upstream)

	require.Equal(t, "blob-A", c.Writer.Header().Get(openAIWSTurnStateHeader))
	raw, ok := svc.openaiCodexTurnStateOrigins.Load("7\x00sess-relay")
	require.True(t, ok)
	origin, ok := raw.(openAICodexTurnStateOrigin)
	require.True(t, ok)
	require.Equal(t, int64(42), origin.accountID)
	require.True(t, origin.expiresAt.After(time.Now()))

	svc.relayOpenAICodexTurnState(c, &Account{ID: 43}, http.Header{})
	require.Empty(t, c.Writer.Header().Get(openAIWSTurnStateHeader))
}

func TestStageOpenAICodexTurnStateRecordsOnlyAfterCommit(t *testing.T) {
	svc := &OpenAIGatewayService{}
	c, _ := newTurnStateTestContext(t, 9, "sess-staged")
	upstream := http.Header{}
	upstream.Set(openAIWSTurnStateHeader, "blob-B")
	var staged http.Header

	stageOpenAICodexTurnState(&staged, upstream)
	require.Equal(t, "blob-B", staged.Get(openAIWSTurnStateHeader))
	_, noted := svc.openaiCodexTurnStateOrigins.Load("9\x00sess-staged")
	require.False(t, noted)

	svc.noteStagedOpenAICodexTurnStateCommitted(c, &Account{ID: 44}, staged)
	raw, ok := svc.openaiCodexTurnStateOrigins.Load("9\x00sess-staged")
	require.True(t, ok)
	origin, ok := raw.(openAICodexTurnStateOrigin)
	require.True(t, ok)
	require.Equal(t, int64(44), origin.accountID)
}

func TestGuardOpenAICodexTurnStateEchoProtectsAccountProvenance(t *testing.T) {
	svc := &OpenAIGatewayService{}
	c, _ := newTurnStateTestContext(t, 7, "sess-guard")
	upstream := http.Header{}
	upstream.Set(openAIWSTurnStateHeader, "blob-A")
	svc.relayOpenAICodexTurnState(c, &Account{ID: 42}, upstream)

	same := http.Header{}
	same.Set(openAIWSTurnStateHeader, "blob-A")
	svc.guardOpenAICodexTurnStateEcho(c, &Account{ID: 42}, same)
	require.Equal(t, "blob-A", same.Get(openAIWSTurnStateHeader))

	foreign := http.Header{}
	foreign[openAIWSTurnStateHeader] = []string{"blob-A"}
	svc.guardOpenAICodexTurnStateEcho(c, &Account{ID: 43}, foreign)
	require.Empty(t, foreign.Get(openAIWSTurnStateHeader))

	svc.openaiCodexTurnStateOrigins.Store("7\x00sess-guard", openAICodexTurnStateOrigin{
		accountID: 42,
		expiresAt: time.Now().Add(-time.Minute),
	})
	expired := http.Header{}
	expired.Set(openAIWSTurnStateHeader, "blob-A")
	svc.guardOpenAICodexTurnStateEcho(c, &Account{ID: 43}, expired)
	require.Equal(t, "blob-A", expired.Get(openAIWSTurnStateHeader))
}

func TestApplyOpenAICodexBetaFeatures(t *testing.T) {
	oauth := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	apiKey := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	t.Run("oauth plain request gets default", func(t *testing.T) {
		c, _ := newTurnStateTestContext(t, 7, "sess-beta")
		headers := http.Header{}
		applyOpenAICodexBetaFeatures(c, oauth, headers)
		require.Equal(t, openAIRemoteCompactionV2Feature, headers.Get("x-codex-beta-features"))
	})

	t.Run("client declaration is preserved", func(t *testing.T) {
		c, _ := newTurnStateTestContext(t, 7, "sess-beta")
		headers := http.Header{"X-Codex-Beta-Features": []string{"some_other_feature"}}
		applyOpenAICodexBetaFeatures(c, oauth, headers)
		require.Equal(t, "some_other_feature", headers.Get("x-codex-beta-features"))
	})

	t.Run("native compaction forces feature for API key", func(t *testing.T) {
		c, _ := newTurnStateTestContext(t, 7, "sess-beta")
		MarkOpenAINativeCompactionV2(c)
		headers := http.Header{"X-Codex-Beta-Features": []string{"some_other_feature"}}
		applyOpenAICodexBetaFeatures(c, apiKey, headers)
		require.Contains(t, headers.Get("x-codex-beta-features"), "some_other_feature")
		require.Contains(t, headers.Get("x-codex-beta-features"), openAIRemoteCompactionV2Feature)
	})

	t.Run("plain API key request is untouched", func(t *testing.T) {
		c, _ := newTurnStateTestContext(t, 7, "sess-beta")
		headers := http.Header{}
		applyOpenAICodexBetaFeatures(c, apiKey, headers)
		require.Empty(t, headers.Get("x-codex-beta-features"))
	})
}

func TestBuildOpenAIWSHeadersCarriesSessionBetaFeatures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	decision := OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}
	build := func(t *testing.T, account *Account, beta string) http.Header {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		if beta != "" {
			c.Request.Header.Set("x-codex-beta-features", beta)
		}
		headers, _, err := svc.buildOpenAIWSHeaders(
			context.Background(), c, account, "test-token", decision,
			true, "", "", "", "gpt-5.6-codex", "",
		)
		require.NoError(t, err)
		return headers
	}

	oauth := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "test-account"},
	}
	require.Equal(t, openAIRemoteCompactionV2Feature, build(t, oauth, "").Get("x-codex-beta-features"))
	require.Equal(t, []string{"some_other_feature"}, build(t, oauth, "some_other_feature").Values("x-codex-beta-features"))
	require.Empty(t, build(t, &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "").Get("x-codex-beta-features"))
}
