//go:build unit

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type nativeWireFixture struct {
	Variant string            `json:"variant"`
	Effort  string            `json:"effort"`
	Headers map[string]string `json:"headers"`
	Body    map[string]any    `json:"body"`
}

// Optional fixtures come only from an isolated Codex with synthetic credentials.
// They stay outside the repository; failures print field hashes, never prompts.
func TestNativeCodexCapturedWireReplay(t *testing.T) {
	path := os.Getenv("XIASS_SYNTHETIC_CODEX_WIRE_FIXTURES")
	if path == "" {
		t.Skip("set XIASS_SYNTHETIC_CODEX_WIRE_FIXTURES to temporary loopback captures")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var fixtures []nativeWireFixture
	require.NoError(t, json.Unmarshal(data, &fixtures))
	require.NotEmpty(t, fixtures)
	for _, fixture := range fixtures {
		t.Run(fixture.Variant+"/"+fixture.Effort, func(t *testing.T) { replayNativeWire(t, fixture) })
	}
}

func TestNativeCodexSyntheticWireReplay(t *testing.T) {
	for _, lite := range []bool{false, true} {
		fixture := nativeWireFixture{Variant: fmt.Sprint("lite=", lite), Headers: map[string]string{
			"User-Agent": "codex_cli_rs/0.153.4", "originator": "codex_cli_rs",
		}, Body: map[string]any{
			"model": "gpt-6-astra", "stream": true, "store": false,
			"reasoning":    map[string]any{"effort": "xhigh", "summary": "auto"},
			"include":      []any{"reasoning.encrypted_content"},
			"instructions": "SYNTHETIC model instructions", "tools": []any{},
			"input": []any{map[string]any{"type": "message", "role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "synthetic task"},
			}}},
		}}
		if lite {
			fixture.Headers[responsesLiteHeader] = "true"
			fixture.Body["parallel_tool_calls"] = false
			delete(fixture.Body, "instructions")
			fixture.Body["reasoning"].(map[string]any)["context"] = "all_turns"
			fixture.Body["input"] = append([]any{
				map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{
					map[string]any{"type": "function", "name": "synthetic_tool", "parameters": map[string]any{"type": "object"}},
				}},
				map[string]any{"type": "message", "role": "developer", "content": []any{
					map[string]any{"type": "input_text", "text": "SYNTHETIC model instructions"},
				}},
			}, fixture.Body["input"].([]any)...)
		}
		t.Run(fixture.Variant, func(t *testing.T) { replayNativeWire(t, fixture) })
	}
}

func TestOpenAIContinuationKeepsToolIDsWithoutCodexBranding(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","stream":true,"instructions":"SYNTHETIC","previous_response_id":"resp_synthetic_prior","input":[{"type":"function_call_output","call_id":"call_synthetic","output":"SYNTHETIC_TOOL_RESULT"}]}`)
	transport := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK,
		Header: http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:   io.NopCloser(strings.NewReader("data: " + string(nativeReplayResponseEvents(t)[3]) + "\n\n"))}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("User-Agent", "Mozilla/5.0 (Hermes/1.0)")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: transport}
	account := &Account{ID: 779, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "synthetic-token"}}
	_, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.Len(t, transport.requests, 1)
	var before, after map[string]any
	require.NoError(t, json.Unmarshal(body, &before))
	require.NoError(t, json.Unmarshal(transport.lastBody, &after))
	for _, field := range []string{"model", "instructions", "previous_response_id", "input"} {
		require.Equal(t, nativeWireDigest(before[field]), nativeWireDigest(after[field]), "field changed: %s", field)
	}
}

func replayNativeWire(t *testing.T, fixture nativeWireFixture) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	for _, scenario := range []string{"initial", "tool-result", "tool-return-only", "compacted-continuation"} {
		for _, upstream := range []string{"http", "websocket"} {
			t.Run(scenario+"/"+upstream, func(t *testing.T) {
				encoded, err := json.Marshal(fixture.Body)
				require.NoError(t, err)
				var body map[string]any
				require.NoError(t, json.Unmarshal(encoded, &body))
				// WS framing is not part of the Responses request body on HTTP.
				delete(body, "type")
				delete(body, "generate")
				body["stream"] = true
				input, ok := body["input"].([]any)
				require.True(t, ok)
				switch scenario {
				case "tool-result":
					body["input"] = append(input,
						map[string]any{"type": "function_call", "call_id": "call_synthetic", "name": "synthetic_tool", "arguments": `{"value":1}`},
						map[string]any{"type": "function_call_output", "call_id": "call_synthetic", "output": "SYNTHETIC_TOOL_RESULT"})
				case "compacted-continuation":
					body["previous_response_id"] = "resp_synthetic_prior"
					body["input"] = append(input, map[string]any{"type": "compaction", "encrypted_content": "SYNTHETIC_OPAQUE_HISTORY"})
				case "tool-return-only":
					body["previous_response_id"] = "resp_synthetic_tool"
					body["input"] = []any{map[string]any{"type": "function_call_output", "call_id": "call_synthetic", "output": "SYNTHETIC_TOOL_RESULT"}}
				}
				encoded, err = json.Marshal(body)
				require.NoError(t, err)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(encoded))
				for k, v := range fixture.Headers {
					c.Request.Header.Set(k, v)
				}
				// Native WS carries this flag in client_metadata; HTTP carries it
				// in a header. Raw WS ingress/bridge is covered separately.
				if isOpenAIResponsesLiteWebSocketPayload(encoded) {
					c.Request.Header.Set(responsesLiteHeader, "true")
				}
				cfg := &config.Config{}
				account := &Account{ID: 778, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
					Status: StatusActive, Schedulable: true, Concurrency: 1,
					Credentials: map[string]any{"access_token": "synthetic-token", "chatgpt_account_id": "synthetic-account"},
					Extra:       map[string]any{codexFingerprintModeExtraKey: "off"}}
				events := nativeReplayResponseEvents(t)
				var stream strings.Builder
				for _, event := range events {
					fmt.Fprintf(&stream, "data: %s\n\n", event)
				}
				httpCapture := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK,
					Header: http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:   io.NopCloser(strings.NewReader(stream.String()))}}
				svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: httpCapture}
				var wsCapture *openAIWSCaptureConn
				if upstream == "websocket" {
					cfg.Gateway.OpenAIWS.Enabled = true
					cfg.Gateway.OpenAIWS.OAuthEnabled = true
					cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
					cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
					cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
					account.Extra["responses_websockets_v2_enabled"] = true
					wsCapture = &openAIWSCaptureConn{events: events}
					pool := newOpenAIWSConnPool(cfg)
					pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: wsCapture})
					t.Cleanup(pool.Close)
					svc.openaiWSPool = pool
					svc.openaiWSResolver = NewOpenAIWSProtocolResolver(cfg)
					svc.cache = &stubGatewayCache{}
				} else {
					SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				}
				result, err := svc.Forward(context.Background(), c, account, encoded)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, "gpt-6-astra", result.UpstreamModel)
				var forwarded map[string]any
				if wsCapture != nil {
					require.True(t, result.OpenAIWSMode)
					require.Empty(t, httpCapture.requests)
					payload, err := json.Marshal(wsCapture.lastWrite)
					require.NoError(t, err)
					require.NoError(t, json.Unmarshal(payload, &forwarded))
				} else {
					require.Len(t, httpCapture.requests, 1)
					require.NoError(t, json.Unmarshal(httpCapture.lastBody, &forwarded))
				}
				for _, field := range []string{"model", "instructions", "input", "tools", "tool_choice", "reasoning", "text", "include", "parallel_tool_calls", "previous_response_id", "prompt_cache_key", "max_output_tokens", "store", "service_tier"} {
					before, existsBefore := body[field]
					after, existsAfter := forwarded[field]
					require.Equal(t, existsBefore, existsAfter, "field presence changed: %s", field)
					require.Equal(t, nativeWireDigest(before), nativeWireDigest(after), "field content changed: %s", field)
				}
				var changed []string
				for key, value := range forwarded {
					if nativeWireDigest(body[key]) != nativeWireDigest(value) {
						changed = append(changed, key)
					}
				}
				sort.Strings(changed)
				t.Logf("outbound added/changed fields: %v", changed)
				for key, value := range body {
					_, present := forwarded[key]
					require.True(t, present, "removed request field: %s", key)
					require.Equal(t, nativeWireDigest(value), nativeWireDigest(forwarded[key]), "request field changed: %s", key)
				}
				if upstream == "websocket" {
					require.Equal(t, "response.create", forwarded["type"])
					require.Equal(t, len(body)+1, len(forwarded), "unexpected added request fields")
				} else {
					require.Equal(t, len(body), len(forwarded), "unexpected added request fields")
				}
				gotEvents := make([]map[string]any, 0, len(events))
				for _, line := range strings.Split(rec.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event map[string]any
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
					gotEvents = append(gotEvents, event)
				}
				require.Len(t, gotEvents, len(events))
				for i, want := range events {
					var event map[string]any
					require.NoError(t, json.Unmarshal(want, &event))
					require.Equal(t, nativeWireDigest(event), nativeWireDigest(gotEvents[i]), "response event %d changed", i)
				}
			})
		}
	}
}

func nativeWireDigest(value any) string {
	data, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func nativeReplayResponseEvents(t *testing.T) [][]byte {
	t.Helper()
	item := map[string]any{"id": "msg_synthetic", "type": "message", "status": "completed", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": "<html>SYNTHETIC_COMPLETE_OUTPUT</html>", "annotations": []any{}}}}
	response := map[string]any{"id": "resp_synthetic_replay", "model": "gpt-6-astra", "status": "completed", "output": []any{item},
		"usage": map[string]any{"input_tokens": 30000, "output_tokens": 5104, "output_tokens_details": map[string]any{"reasoning_tokens": 4096}}}
	var events [][]byte
	for _, event := range []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": response["id"], "model": "gpt-6-astra", "status": "in_progress", "output": []any{}}},
		{"type": "response.output_text.delta", "item_id": item["id"], "output_index": 0, "content_index": 0, "delta": "<html>SYNTHETIC_COMPLETE_OUTPUT</html>"},
		{"type": "response.output_item.done", "output_index": 0, "item": item},
		{"type": "response.completed", "response": response},
	} {
		data, err := json.Marshal(event)
		require.NoError(t, err)
		events = append(events, data)
	}
	return events
}
