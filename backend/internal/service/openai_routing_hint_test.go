package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSetOpenAICodexRoutingHintCanonicalizesOfficialServiceTiers(t *testing.T) {
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	tests := []struct {
		name  string
		model string
		tier  string
		want  string
	}{
		{name: "fast alias", model: "gpt-5.6", tier: " fast ", want: "model=gpt-5.6;tier=priority"},
		{name: "priority", model: "gpt-5.6", tier: "priority", want: "model=gpt-5.6;tier=priority"},
		{name: "flex", model: "gpt-5.6", tier: "flex", want: "model=gpt-5.6;tier=flex"},
		{name: "default", model: "gpt-5.6", tier: "default", want: "model=gpt-5.6"},
		{name: "omitted", model: "gpt-5.6", want: "model=gpt-5.6"},
		{name: "unknown", model: "gpt-5.6", tier: "turbo", want: "model=gpt-5.6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := make(http.Header)
			setOpenAICodexRoutingHint(headers, oauth, tt.model, tt.tier)
			require.Equal(t, tt.want, headers.Get(openAICodexRoutingHintHeader))
		})
	}

	for _, model := range []string{"gpt-5.6;evil", "gpt=5.6", "gpt-5.6\ninvalid"} {
		t.Run("invalid model "+model, func(t *testing.T) {
			headers := make(http.Header)
			setOpenAICodexRoutingHint(headers, oauth, model, "priority")
			require.Empty(t, headers.Get(openAICodexRoutingHintHeader))
		})
	}
}

func TestSetOpenAICodexRoutingHintStripsSpoofedValues(t *testing.T) {
	headers := make(http.Header)
	headers[openAICodexRoutingHintHeader] = []string{"lowercase-spoof"}
	headers["X-Codex-Routing-Hint"] = []string{"canonical-spoof"}
	setOpenAICodexRoutingHint(headers, &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "gpt-5.6", "priority")
	for key := range headers {
		require.False(t, strings.EqualFold(key, openAICodexRoutingHintHeader))
	}

	headers[openAICodexRoutingHintHeader] = []string{"model=spoof;tier=flex"}
	setOpenAICodexRoutingHint(headers, &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, "gpt-5.6", "priority")
	require.Equal(t, "model=gpt-5.6;tier=priority", headers.Get(openAICodexRoutingHintHeader))
}

func TestOpenAIOAuthHTTPBuildersSendRoutingHintFromFinalBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "test-account",
		},
	}
	svc := &OpenAIGatewayService{}
	tests := []struct {
		body []byte
		want string
	}{
		{body: []byte(`{"model":"gpt-5.6-codex","service_tier":"fast"}`), want: "model=gpt-5.6-codex;tier=priority"},
		{body: []byte(`{"model":"gpt-5.6-codex","service_tier":"flex"}`), want: "model=gpt-5.6-codex;tier=flex"},
		{body: []byte(`{"model":"gpt-5.6-codex","service_tier":"default"}`), want: "model=gpt-5.6-codex"},
	}

	for _, tt := range tests {
		for _, passthrough := range []bool{false, true} {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(tt.body))

			var req *http.Request
			var err error
			if passthrough {
				req, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, tt.body, "test-token")
			} else {
				req, err = svc.buildUpstreamRequest(context.Background(), c, account, tt.body, "test-token", false, "", true)
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, req.Header.Get(openAICodexRoutingHintHeader))
		}
	}
}

func TestStripOpenAILegacyResponsesBetaPreservesIndependentTokens(t *testing.T) {
	headers := make(http.Header)
	headers["openai-beta"] = []string{
		"responses=experimental, future_feature=v1",
		"another_feature=v2, RESPONSES=EXPERIMENTAL",
	}

	stripOpenAILegacyResponsesBeta(headers)

	require.Equal(t, []string{"future_feature=v1", "another_feature=v2"}, headers.Values("OpenAI-Beta"))
}

func TestBuildOpenAIWSHeadersSendsOAuthRoutingHintOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	svc := &OpenAIGatewayService{}
	decision := OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}

	build := func(t *testing.T, account *Account, tier string) http.Header {
		t.Helper()
		headers, _, err := svc.buildOpenAIWSHeaders(
			context.Background(), c, account, "test-token", decision, true,
			"", "", "", "gpt-5.6-codex", tier,
		)
		require.NoError(t, err)
		return headers
	}

	oauth := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "test-account",
		},
	}
	require.Equal(t, "model=gpt-5.6-codex;tier=priority", build(t, oauth, "fast").Get(openAICodexRoutingHintHeader))
	require.Equal(t, "model=gpt-5.6-codex", build(t, oauth, "default").Get(openAICodexRoutingHintHeader))
	require.Empty(t, build(t, &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "priority").Get(openAICodexRoutingHintHeader))
}
