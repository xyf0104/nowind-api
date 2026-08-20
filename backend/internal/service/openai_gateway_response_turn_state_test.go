package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIStreamingResponseCommitsStagedTurnState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	upstreamHeaders := http.Header{}
	upstreamHeaders.Set(openAIWSTurnStateHeader, "turn-state-1")
	upstreamHeaders.Set("X-Request-Id", "rid-turn-state")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     upstreamHeaders,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			`data: {"type":"response.output_text.delta","delta":"hello"}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n"))),
	}

	result, err := service.handleStreamingResponse(
		context.Background(), resp, c,
		&Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		time.Now(), "gpt-test", "gpt-test",
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "turn-state-1", recorder.Header().Get(openAIWSTurnStateHeader))
}

func TestOpenAINonStreamingResponseRelaysTurnState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &OpenAIGatewayService{cfg: &config.Config{}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	upstreamHeaders := http.Header{}
	upstreamHeaders.Set("Content-Type", "application/json")
	upstreamHeaders.Set(openAIWSTurnStateHeader, "turn-state-json")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     upstreamHeaders,
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_json","model":"gpt-test","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
	}

	result, err := service.handleNonStreamingResponse(
		context.Background(), resp, c,
		&Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		"gpt-test", "gpt-test",
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "turn-state-json", recorder.Header().Get(openAIWSTurnStateHeader))
}

func TestOpenAISSEToJSONRelaysTurnState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &OpenAIGatewayService{cfg: &config.Config{}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	upstreamHeaders := http.Header{}
	upstreamHeaders.Set("Content-Type", "text/event-stream")
	upstreamHeaders.Set(openAIWSTurnStateHeader, "turn-state-sse-json")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     upstreamHeaders,
	}
	body := []byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_sse\",\"model\":\"gpt-test\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")

	result, err := service.handleSSEToJSON(
		resp, c,
		&Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		body, "gpt-test", "gpt-test",
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "turn-state-sse-json", recorder.Header().Get(openAIWSTurnStateHeader))
}
