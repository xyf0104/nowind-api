//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const openAINativePartialUsageFailureSSE = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-partial-native\",\"model\":\"gpt-5.4\",\"status\":\"in_progress\",\"output\":[]}}\n\n" +
	"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
	"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp-partial-native\",\"object\":\"response\",\"model\":\"gpt-5.4\",\"status\":\"failed\",\"output\":[],\"usage\":{\"input_tokens\":11,\"output_tokens\":5,\"total_tokens\":16},\"error\":{\"code\":\"context_window_exceeded\",\"message\":\"input exceeds the context window\"}}}\n\n"

func newNativeOpenAIPartialUsageAccount() *Account {
	return &Account{
		ID:          982,
		Name:        "openai-native-partial-usage",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "test-access-token",
			"chatgpt_account_id": "test-chatgpt-account",
		},
	}
}

func TestOpenAIGatewayService_Forward_NativeStreamFailurePreservesPartialUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"rid-native-partial"},
		},
		Body: io.NopCloser(strings.NewReader(openAINativePartialUsageFailureSSE)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream: upstream,
	}

	result, err := svc.Forward(context.Background(), c, newNativeOpenAIPartialUsageAccount(), body)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, "resp-partial-native", result.ResponseID)
	require.Equal(t, "rid-native-partial", result.RequestID)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.NotNil(t, result.FirstTokenMs)
	require.Equal(t, "rid-native-partial", result.ResponseHeaders.Get("X-Request-Id"))
}

func TestOpenAIGatewayService_Forward_NativeStreamFailoverKeepsNilResult(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"error\",\"error\":{\"message\":\"rate limit exceeded\"}}\n\n")),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream: upstream,
	}

	result, err := svc.Forward(context.Background(), c, newNativeOpenAIPartialUsageAccount(), body)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Nil(t, result)
}
