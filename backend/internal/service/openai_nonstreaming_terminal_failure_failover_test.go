package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func newNonStreamingTerminalFailoverContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return context, recorder
}

func newNonStreamingTerminalFailoverService() *OpenAIGatewayService {
	return &OpenAIGatewayService{cfg: &config.Config{}}
}

func newNonStreamingTerminalFailoverAccount() *Account {
	return &Account{
		ID:       1,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Name:     "pool-account",
		Credentials: map[string]any{
			"pool_mode": true,
		},
	}
}

func newNonStreamingTerminalSSEResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"rid-nonstreaming-failed"},
		},
	}
}

func nonStreamingTerminalSSE(eventType, data string) []byte {
	return []byte(strings.Join([]string{
		"event: " + eventType,
		"data: " + data,
		"",
		"data: [DONE]",
	}, "\n"))
}

func TestNonStreamingSSEToJSONTerminalCapacityFailureFailsOver(t *testing.T) {
	context, recorder := newNonStreamingTerminalFailoverContext(t)
	service := newNonStreamingTerminalFailoverService()
	body := nonStreamingTerminalSSE("response.failed", `{"type":"response.failed","error":{"message":"Selected model is at capacity. Please try a different model.","type":"invalid_request_error"}}`)

	result, err := service.handleSSEToJSON(newNonStreamingTerminalSSEResponse(), context, newNonStreamingTerminalFailoverAccount(), body, "model", "model")

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.Contains(t, string(failoverErr.ResponseBody), "Selected model is at capacity")
	require.False(t, context.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestNonStreamingSSEToJSONTerminalNonRetryableFailureWritesProtocolError(t *testing.T) {
	context, recorder := newNonStreamingTerminalFailoverContext(t)
	service := newNonStreamingTerminalFailoverService()
	body := nonStreamingTerminalSSE("response.failed", `{"type":"response.failed","error":{"type":"content_policy_violation","message":"blocked by policy"}}`)

	result, err := service.handleSSEToJSON(newNonStreamingTerminalSSEResponse(), context, newNonStreamingTerminalFailoverAccount(), body, "model", "model")

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), "blocked by policy")
}

func TestNonStreamingSSEToJSONTerminalBareErrorUsesConservativeClassifier(t *testing.T) {
	t.Run("non transient error stays local", func(t *testing.T) {
		context, recorder := newNonStreamingTerminalFailoverContext(t)
		service := newNonStreamingTerminalFailoverService()
		data := `{"type":"error","error":{"message":"upstream rejected request"}}`

		result, err := service.handleSSEToJSON(newNonStreamingTerminalSSEResponse(), context, newNonStreamingTerminalFailoverAccount(), nonStreamingTerminalSSE("error", data), "model", "model")

		require.Nil(t, result)
		require.Error(t, err)
		var failoverErr *UpstreamFailoverError
		require.False(t, errors.As(err, &failoverErr))
		require.Equal(t, http.StatusBadGateway, recorder.Code)
	})

	t.Run("transient error fails over", func(t *testing.T) {
		context, _ := newNonStreamingTerminalFailoverContext(t)
		service := newNonStreamingTerminalFailoverService()
		data := `{"type":"error","error":{"status_code":429,"message":"rate limited"}}`

		result, err := service.handleSSEToJSON(newNonStreamingTerminalSSEResponse(), context, newNonStreamingTerminalFailoverAccount(), nonStreamingTerminalSSE("error", data), "model", "model")

		require.Nil(t, result)
		var failoverErr *UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.False(t, context.Writer.Written())
	})
}

func TestNonStreamingPassthroughSSEToJSONTerminalCapacityFailureFailsOver(t *testing.T) {
	context, recorder := newNonStreamingTerminalFailoverContext(t)
	service := newNonStreamingTerminalFailoverService()
	body := nonStreamingTerminalSSE("response.failed", `{"type":"response.failed","error":{"message":"Selected model is at capacity. Please try a different model.","type":"invalid_request_error"}}`)

	result, err := service.handlePassthroughSSEToJSON(newNonStreamingTerminalSSEResponse(), context, newNonStreamingTerminalFailoverAccount(), body, "model", "model")

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Contains(t, string(failoverErr.ResponseBody), "Selected model is at capacity")
	require.False(t, context.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestNonStreamingTerminalFailureDoesNotFailoverAfterCommit(t *testing.T) {
	context, recorder := newNonStreamingTerminalFailoverContext(t)
	service := newNonStreamingTerminalFailoverService()
	MarkResponseCommitted(context)
	body := nonStreamingTerminalSSE("response.failed", `{"type":"response.failed","error":{"message":"Selected model is at capacity. Please try a different model."}}`)

	result, err := service.handleSSEToJSON(newNonStreamingTerminalSSEResponse(), context, newNonStreamingTerminalFailoverAccount(), body, "model", "model")

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusBadGateway, recorder.Code)
}
