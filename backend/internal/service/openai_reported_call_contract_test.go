//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Reproduce only the reported request's public protocol shape. All credentials,
// input, output and identifiers are synthetic; the transport never uses a socket.
func TestReportedAstraOAuthHTTPCallRetainsModelContextAndOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, effort := range []string{"xhigh", "max", "ultra"} {
		t.Run(effort, func(t *testing.T) {
			input := "SYNTHETIC_START " + strings.Repeat("context ", 6000) + " SYNTHETIC_END"
			body, err := json.Marshal(map[string]any{
				"model": "gpt-6-astra", "stream": true,
				"instructions": "Preserve this synthetic client instruction exactly.",
				"reasoning":    map[string]any{"effort": effort},
				"input": []any{
					map[string]any{"type": "compaction", "encrypted_content": "synthetic-encrypted-history"},
					map[string]any{"type": "message", "role": "user", "content": input},
				},
			})
			require.NoError(t, err)
			output := `<html><body><p>synthetic complete output</p></body></html>`
			terminal, err := json.Marshal(map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp_synthetic_reported_call", "model": "gpt-6-astra", "status": "completed",
					"output": []any{map[string]any{"id": "msg_synthetic", "type": "message", "role": "assistant",
						"content": []any{map[string]any{"type": "output_text", "text": output}}}},
					"usage": map[string]any{"input_tokens": 629, "output_tokens": 5104},
				},
			})
			require.NoError(t, err)
			transport := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: " + string(terminal) + "\n\n")),
			}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("User-Agent", "Mozilla/5.0 (Hermes/1.0)")
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			account := &Account{ID: 771, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "synthetic-token", "chatgpt_account_id": "synthetic-account",
					"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}},
				Extra: map[string]any{}, Concurrency: 8}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: transport}
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, transport.requests, 1)
			require.Equal(t, "https://chatgpt.com/backend-api/codex/responses", transport.lastReq.URL.String())
			require.Empty(t, transport.lastProxyURL)
			require.Equal(t, "gpt-6-astra", gjson.GetBytes(transport.lastBody, "model").String())
			require.Equal(t, effort, gjson.GetBytes(transport.lastBody, "reasoning.effort").String())
			require.Equal(t, "Preserve this synthetic client instruction exactly.", gjson.GetBytes(transport.lastBody, "instructions").String())
			require.Equal(t, "synthetic-encrypted-history", gjson.GetBytes(transport.lastBody, "input.0.encrypted_content").String())
			require.Equal(t, input, gjson.GetBytes(transport.lastBody, "input.1.content").String())
			final, ok := extractCodexFinalResponse(recorder.Body.String())
			require.True(t, ok)
			require.Equal(t, output, gjson.GetBytes(final, "output.0.content.0.text").String())
			require.Equal(t, "gpt-6-astra", result.UpstreamModel)
		})
	}
}

func TestNativeCodexLiteDoesNotAddLegacyModelInstructions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-6-astra","stream":true,"reasoning":{"effort":"xhigh","context":"all_turns"},"input":[{"type":"additional_tools","role":"developer","tools":[{"type":"function","name":"synthetic_tool","parameters":{"type":"object","properties":{}}}]},{"type":"message","role":"developer","content":[{"type":"input_text","text":"EXACT SYNTHETIC MODEL INSTRUCTIONS"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"synthetic task"}]}]}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.153.4")
	c.Request.Header.Set(responsesLiteHeader, "true")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	transport := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"synthetic capture complete"}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: transport}
	account := &Account{ID: 772, Type: AccountTypeOAuth, Platform: PlatformOpenAI,
		Credentials: map[string]any{"access_token": "synthetic-token", "chatgpt_account_id": "synthetic-account"}}
	_, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err)
	require.Len(t, transport.requests, 1)
	require.False(t, gjson.GetBytes(transport.lastBody, "instructions").Exists())
	require.Equal(t, "gpt-6-astra", gjson.GetBytes(transport.lastBody, "model").String())
	require.Equal(t, "all_turns", gjson.GetBytes(transport.lastBody, "reasoning.context").String())
	require.Equal(t, "synthetic_tool", gjson.GetBytes(transport.lastBody, "input.0.tools.0.name").String())
	require.Equal(t, "EXACT SYNTHETIC MODEL INSTRUCTIONS", gjson.GetBytes(transport.lastBody, "input.1.content.0.text").String())
	require.Equal(t, "synthetic task", gjson.GetBytes(transport.lastBody, "input.2.content.0.text").String())
}
