package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func legacyContentModerationBlockDecision() *securityaudit.Decision {
	return &securityaudit.Decision{
		Kind:           securityaudit.DecisionBlock,
		HTTPStatus:     http.StatusForbidden,
		ErrorCode:      "content_policy_violation",
		ClientMessage:  "内容审计命中风险规则，请调整输入后重试",
		AllowNextStage: false,
		Legacy: &securityaudit.LegacyDecision{
			Blocked:    true,
			Flagged:    true,
			StatusCode: http.StatusForbidden,
			ErrorCode:  "content_policy_violation",
			Message:    "内容审计命中风险规则，请调整输入后重试",
			Action:     "keyword_block",
		},
	}
}

func TestOpenAISecurityAuditBlockReturnsCompletedResponsesObject(t *testing.T) {
	c, recorder := securityAuditErrorTestContext(t)
	c.Request = newTestRequest(t, "/v1/responses")
	setOpsRequestContext(c, "gpt-5.6-sol", false)

	(&OpenAIGatewayHandler{}).openAISecurityAuditError(c, legacyContentModerationBlockDecision())

	require.Equal(t, http.StatusOK, recorder.Code)
	payload := gjson.ParseBytes(recorder.Body.Bytes())
	require.Equal(t, "response", payload.Get("object").String())
	require.Equal(t, "completed", payload.Get("status").String())
	require.Equal(t, "gpt-5.6-sol", payload.Get("model").String())
	require.Greater(t, payload.Get("created_at").Int(), int64(0))
	require.Equal(t, "assistant", payload.Get("output.0.role").String())
	require.Equal(t, "内容审计命中风险规则，请调整输入后重试", payload.Get("output.0.content.0.text").String())
	require.False(t, payload.Get("error").Exists(), "completed responses must not contain an error object")
	require.False(t, payload.Get("status_code").Exists(), "completed responses must not expose an HTTP status code")
	require.False(t, payload.Get("output.0.status_code").Exists(), "completed output must not expose an HTTP status code")
}

func TestGatewayResponsesSecurityAuditBlockReturnsCompletedResponsesObject(t *testing.T) {
	c, recorder := securityAuditErrorTestContext(t)
	c.Request = newTestRequest(t, "/v1/responses")
	setOpsRequestContext(c, "claude-opus-4-6", false)

	(&GatewayHandler{}).responsesSecurityAuditError(c, legacyContentModerationBlockDecision())

	require.Equal(t, http.StatusOK, recorder.Code)
	payload := gjson.ParseBytes(recorder.Body.Bytes())
	require.Equal(t, "response", payload.Get("object").String())
	require.Equal(t, "completed", payload.Get("status").String())
	require.Equal(t, "内容审计命中风险规则，请调整输入后重试", payload.Get("output.0.content.0.text").String())
}

func TestOpenAISecurityAuditBlockReturnsCompletedResponsesStream(t *testing.T) {
	c, recorder := securityAuditErrorTestContext(t)
	c.Request = newTestRequest(t, "/v1/responses")
	setOpsRequestContext(c, "gpt-5.6-sol", true)

	(&OpenAIGatewayHandler{}).openAISecurityAuditError(c, legacyContentModerationBlockDecision())

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	require.Contains(t, body, "event: response.created\n")
	require.Contains(t, body, "event: response.output_text.delta\n")
	require.Contains(t, body, "event: response.completed\n")
	require.Contains(t, body, `"created_at":`)
	require.NotContains(t, body, "response.failed")
	require.NotContains(t, body, "event: error")
	require.Equal(t, "内容审计命中风险规则，请调整输入后重试", lastResponsesCompletionText(t, body))
}

func TestOpenAISecurityAuditBlockReturnsCompletedChatCompletion(t *testing.T) {
	c, recorder := securityAuditErrorTestContext(t)
	c.Request = newTestRequest(t, "/v1/chat/completions")
	setOpsRequestContext(c, "gpt-5.6-sol", false)

	(&OpenAIGatewayHandler{}).openAISecurityAuditError(c, legacyContentModerationBlockDecision())

	require.Equal(t, http.StatusOK, recorder.Code)
	payload := gjson.ParseBytes(recorder.Body.Bytes())
	require.Equal(t, "chat.completion", payload.Get("object").String())
	require.Equal(t, "assistant", payload.Get("choices.0.message.role").String())
	require.Equal(t, "内容审计命中风险规则，请调整输入后重试", payload.Get("choices.0.message.content").String())
	require.Equal(t, "stop", payload.Get("choices.0.finish_reason").String())
}

func TestOpenAISecurityAuditBlockReturnsCompletedChatStream(t *testing.T) {
	c, recorder := securityAuditErrorTestContext(t)
	c.Request = newTestRequest(t, "/v1/chat/completions")
	setOpsRequestContext(c, "gpt-5.6-sol", true)

	(&OpenAIGatewayHandler{}).openAISecurityAuditError(c, legacyContentModerationBlockDecision())

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	require.Contains(t, body, "chat.completion.chunk")
	require.Contains(t, body, "内容审计命中风险规则，请调整输入后重试")
	require.True(t, strings.HasSuffix(body, "data: [DONE]\n\n"))
}

func newTestRequest(t *testing.T, endpoint string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "http://example.test"+endpoint, nil)
	require.NoError(t, err)
	return request
}

func lastResponsesCompletionText(t *testing.T, body string) string {
	t.Helper()
	for _, frame := range strings.Split(body, "\n\n") {
		if !strings.Contains(frame, "event: response.completed") {
			continue
		}
		for _, line := range strings.Split(frame, "\n") {
			if strings.HasPrefix(line, "data: ") {
				return gjson.Get(strings.TrimPrefix(line, "data: "), "response.output.0.content.0.text").String()
			}
		}
	}
	t.Fatal("response.completed frame not found")
	return ""
}
