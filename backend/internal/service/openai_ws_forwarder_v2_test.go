package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForwardOpenAIWSV2CyberPolicyShortCircuitsBeforeRateLimitAndRawWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		upstreamEvent []byte
		stream        bool
		wantInput     int
		wantOutput    int
	}{
		{
			name:          "non_stream_error_disguised_as_rate_limit",
			upstreamEvent: []byte(`{"type":"error","error":{"type":"rate_limit_error","code":"cyber_policy","message":"blocked raw payload"},"usage":{"input_tokens":5,"output_tokens":1}}`),
			wantInput:     5,
			wantOutput:    1,
		},
		{
			name:          "stream_response_failed_disguised_as_rate_limit",
			upstreamEvent: []byte(`{"type":"response.failed","response":{"id":"resp_cyber","status":"failed","error":{"type":"rate_limit_error","code":"cyber_policy","message":"blocked raw payload"},"usage":{"input_tokens":9,"output_tokens":2}}}`),
			stream:        true,
			wantInput:     9,
			wantOutput:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)

			cfg := newOpenAIWSV2TestConfig()
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			captureConn := &openAIWSCaptureConn{events: [][]byte{append([]byte(nil), tt.upstreamEvent...)}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: captureConn})
			rateLimitRepo := &openAIWSRateLimitSignalRepo{}
			svc := &OpenAIGatewayService{
				cfg:              cfg,
				httpUpstream:     &httpUpstreamRecorder{},
				cache:            &stubGatewayCache{},
				rateLimitService: &RateLimitService{accountRepo: rateLimitRepo},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
				openaiWSPool:     pool,
			}
			account := &Account{
				ID: 5883, Name: "openai-ws-v2-cyber", Platform: PlatformOpenAI,
				Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra:       map[string]any{"responses_websockets_v2_enabled": true},
			}

			requestBody := []byte(`{"model":"gpt-5.5","stream":false,"input":"hello"}`)
			if tt.stream {
				requestBody = []byte(`{"model":"gpt-5.5","stream":true,"input":"hello"}`)
			}
			result, err := svc.Forward(context.Background(), c, account, requestBody)
			require.ErrorIs(t, err, ErrOpenAIWSCyberPolicyBlocked)
			require.Nil(t, result)
			var failoverErr *UpstreamFailoverError
			require.False(t, errors.As(err, &failoverErr))

			responseBody := recorder.Body.String()
			require.Equal(t, 1, strings.Count(responseBody, OpenAIWSCyberPolicyClientMessage))
			require.NotContains(t, responseBody, "blocked raw payload")
			if tt.stream {
				require.Contains(t, responseBody, "data: ")
			} else {
				require.Equal(t, http.StatusBadRequest, recorder.Code)
			}
			mark := GetOpsCyberPolicy(c)
			require.NotNil(t, mark)
			require.Equal(t, http.StatusOK, mark.UpstreamStatus)
			require.Equal(t, tt.wantInput, mark.UpstreamInTok)
			require.Equal(t, tt.wantOutput, mark.UpstreamOutTok)
			require.Empty(t, rateLimitRepo.rateLimitCalls)
			require.Empty(t, rateLimitRepo.updateExtra)
			require.Equal(t, StatusActive, account.Status)
			require.Nil(t, account.RateLimitResetAt)
			require.Nil(t, account.TempUnschedulableUntil)
		})
	}
}

func TestOpenAIWSV2PassthroughCyberPolicyUsesSharedEventMarker(t *testing.T) {
	t.Parallel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	payload := []byte(`{"type":"error","error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":7,"output_tokens":3}}`)
	require.True(t, markOpenAIWSV2PassthroughCyberPolicy(c, payload))
	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark)
	require.Equal(t, http.StatusOK, mark.UpstreamStatus)
	require.Equal(t, 7, mark.UpstreamInTok)
	require.Equal(t, 3, mark.UpstreamOutTok)
}
