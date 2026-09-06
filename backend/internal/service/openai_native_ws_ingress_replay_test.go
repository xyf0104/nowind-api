//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const nativeWSReplayLiteMarker = "ws_request_header_x_openai_internal_codex_responses_lite"

// Capture files remain outside the repository and contain only isolated,
// synthetic Codex traffic. A tool-return capture must run on its original session.
func TestNativeCodexCapturedWSIngressReplay(t *testing.T) {
	path := os.Getenv("XIASS_SYNTHETIC_CODEX_WIRE_FIXTURES")
	if path == "" {
		t.Skip("set XIASS_SYNTHETIC_CODEX_WIRE_FIXTURES to synthetic native WS captures")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var fixtures []nativeWireFixture
	require.NoError(t, json.Unmarshal(data, &fixtures))
	require.Positive(t, len(fixtures))
	used := make([]bool, len(fixtures))
	for i, fixture := range fixtures {
		if strings.HasSuffix(fixture.Variant, "-tool-return") {
			continue
		}
		used[i] = true
		var continuation *nativeWireFixture
		for j := range fixtures {
			if fixtures[j].Variant == fixture.Variant+"-tool-return" && fixtures[j].Effort == fixture.Effort {
				require.True(t, continuation == nil, "duplicate continuation capture")
				continuation = &fixtures[j]
				used[j] = true
			}
		}
		t.Run(fixture.Variant+"/"+fixture.Effort, func(t *testing.T) {
			replayNativeWSIngressCases(t, fixture, continuation)
		})
	}
	for i, consumed := range used {
		require.True(t, consumed, "orphan continuation capture at index %d", i)
	}
}

func TestNativeCodexSyntheticWSIngressReplay(t *testing.T) {
	for _, lite := range []bool{false, true} {
		for _, effort := range []string{"max", "xhigh"} {
			t.Run(fmt.Sprintf("lite=%t/%s", lite, effort), func(t *testing.T) {
				clockTool := map[string]any{"type": "namespace", "name": "clock", "tools": []any{
					map[string]any{"type": "function", "name": "sleep", "parameters": map[string]any{
						"type": "object", "properties": map[string]any{"duration_ms": map[string]any{"type": "number"}},
						"required": []any{"duration_ms"}, "additionalProperties": false,
					}},
				}}
				body := map[string]any{
					"type": "response.create", "model": "gpt-6-astra", "stream": true, "store": false,
					"instructions": "SYNTHETIC model instructions", "parallel_tool_calls": true,
					"reasoning": map[string]any{"effort": effort}, "include": []any{"reasoning.encrypted_content"},
					"text": map[string]any{"verbosity": "low"}, "tool_choice": "auto",
					"prompt_cache_key": "synthetic-ws-replay", "client_metadata": map[string]any{"turn_id": "synthetic-turn"},
					"tools": []any{clockTool, map[string]any{"type": "custom", "name": "exec"}},
					"input": []any{map[string]any{"type": "message", "role": "user", "content": []any{
						map[string]any{"type": "input_text", "text": "SYNTHETIC task"},
					}}},
				}
				if lite {
					delete(body, "instructions")
					delete(body, "tools")
					body["parallel_tool_calls"] = false
					body["reasoning"].(map[string]any)["context"] = "all_turns"
					body["client_metadata"].(map[string]any)[nativeWSReplayLiteMarker] = "true"
					body["input"] = append([]any{
						map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{clockTool}},
						map[string]any{"type": "message", "role": "developer", "content": []any{
							map[string]any{"type": "input_text", "text": "SYNTHETIC model instructions"},
						}},
					}, body["input"].([]any)...)
				}
				fixture := nativeWireFixture{Body: body, Headers: map[string]string{
					"User-Agent": "codex_cli_rs/0.153.4", "originator": "codex_cli_rs",
					"OpenAI-Beta": "responses_websockets=2026-02-06",
				}}
				followup := nativeWireFixture{Headers: fixture.Headers, Body: nativeWSReplayClone(t, body)}
				followup.Body["previous_response_id"] = "resp_audit_tool"
				followup.Body["input"] = []any{map[string]any{
					"type": "function_call_output", "call_id": "call_synthetic_audit", "output": "SYNTHETIC sleep completed",
				}}
				replayNativeWSIngressCases(t, fixture, &followup)
			})
		}
	}
}

type nativeWSReplayTurn struct {
	frame     map[string]any
	events    [][]byte
	httpInput []any
}

func replayNativeWSIngressCases(t *testing.T, fixture nativeWireFixture, continuation *nativeWireFixture) {
	t.Helper()
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModeHTTPBridge} {
		t.Run(mode, func(t *testing.T) {
			name := "initial"
			turns := []nativeWSReplayTurn{{frame: fixture.Body, events: nativeReplayResponseEvents(t)}}
			if continuation != nil {
				name = "native-tool-continuation"
				call, events := nativeWSReplayToolEvents(t)
				turns[0].events = events
				input, ok := continuation.Body["input"].([]any)
				require.True(t, ok)
				require.Equal(t, 1, len(input), "native continuation must contain only its tool result")
				output, ok := input[0].(map[string]any)
				require.True(t, ok)
				require.Equal(t, nativeWireDigest("function_call_output"), nativeWireDigest(output["type"]))
				require.Equal(t, nativeWireDigest(call["call_id"]), nativeWireDigest(output["call_id"]))
				require.Equal(t, nativeWireDigest("resp_audit_tool"), nativeWireDigest(continuation.Body["previous_response_id"]))
				initialInput, ok := fixture.Body["input"].([]any)
				require.True(t, ok)
				// Independent semantic oracle: the HTTP bridge must replay the
				// original input, the upstream call, and the unmodified call result.
				httpInput := append(append([]any{}, initialInput...), call)
				httpInput = append(httpInput, input...)
				turns = append(turns, nativeWSReplayTurn{
					frame: continuation.Body, events: nativeReplayResponseEvents(t), httpInput: httpInput,
				})
			}
			t.Run(name, func(t *testing.T) { replayNativeWSIngress(t, mode, fixture.Headers, turns) })
			t.Run("compacted-continuation", func(t *testing.T) {
				body := nativeWSReplayClone(t, fixture.Body)
				input, ok := body["input"].([]any)
				require.True(t, ok)
				body["input"] = append(input, map[string]any{
					"type": "compaction", "encrypted_content": "SYNTHETIC_OPAQUE_HISTORY",
				})
				body["previous_response_id"] = "resp_synthetic_replay"
				// store=false continuation requires the live first-turn lease.
				replayNativeWSIngress(t, mode, fixture.Headers, []nativeWSReplayTurn{
					{frame: fixture.Body, events: nativeReplayResponseEvents(t)},
					{frame: body, events: nativeReplayResponseEvents(t)},
				})
			})
		})
	}
}

func replayNativeWSIngress(t *testing.T, mode string, headers map[string]string, turns []nativeWSReplayTurn) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 5
	account := &Account{
		ID: 779, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "synthetic-token", "chatgpt_account_id": "synthetic-account"},
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_mode": mode,
			codexFingerprintModeExtraKey:                "off",
			"codex_image_generation_bridge":             false,
		},
	}
	wsCapture := &openAIWSCaptureConn{}
	httpCapture := &httpUpstreamRecorder{}
	for _, turn := range turns {
		var sse strings.Builder
		for _, event := range turn.events {
			wsCapture.events = append(wsCapture.events, append([]byte(nil), event...))
			fmt.Fprintf(&sse, "data: %s\n\n", event)
		}
		httpCapture.responses = append(httpCapture.responses, &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(sse.String())),
		})
	}
	// Both outbound transports are fakes, even if mode selection regresses.
	dialer := &openAIWSCaptureDialer{conn: wsCapture}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	defer pool.Close()
	svc := &OpenAIGatewayService{
		cfg: cfg, httpUpstream: httpCapture, cache: &stubGatewayCache{},
		openaiWSPool: pool, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(),
	}
	type turnResult struct {
		turn   int
		result *OpenAIForwardResult
		err    error
	}
	results := make(chan turnResult, len(turns))
	hooks := &OpenAIWSIngressHooks{AfterTurn: func(turn int, result *OpenAIForwardResult, err error) {
		results <- turnResult{turn: turn, result: result, err: err}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(16 << 20)
		_, firstFrame, err := conn.Read(ctx)
		if err != nil {
			serverErr <- err
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r.Clone(ctx)
		serverErr <- svc.ProxyResponsesWebSocketFromClient(ctx, c, conn, account, "synthetic-token", firstFrame, hooks)
	}))
	defer server.Close()
	header := make(http.Header)
	for key, value := range headers {
		header.Set(key, value)
	}
	require.Empty(t, header.Get(responsesLiteHeader), "native WS Lite must use its body marker, not an injected HTTP header")
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", &coderws.DialOptions{HTTPHeader: header})
	require.NoError(t, err)
	client.SetReadLimit(16 << 20)
	finished := false
	defer func() {
		_ = client.CloseNow()
		cancel()
		if !finished {
			select {
			case <-serverErr:
			case <-time.After(5 * time.Second):
				t.Error("native WS ingress did not exit during cleanup")
			}
		}
	}()
	for i, turn := range turns {
		frame, err := json.Marshal(turn.frame)
		require.NoError(t, err)
		require.NoError(t, client.Write(ctx, coderws.MessageText, frame))
		for j, expected := range turn.events {
			kind, actual, err := client.Read(ctx)
			require.NoError(t, err, "turn %d event %d", i+1, j)
			require.Equal(t, coderws.MessageText, kind)
			var want, got map[string]any
			require.NoError(t, json.Unmarshal(expected, &want))
			require.NoError(t, json.Unmarshal(actual, &got))
			assert.Equal(t, nativeWireDigest(want), nativeWireDigest(got), "turn %d response event %d changed", i+1, j)
		}
		select {
		case result := <-results:
			require.NoError(t, result.err)
			require.NotNil(t, result.result)
			require.Equal(t, i+1, result.turn)
			assert.Equal(t, nativeWireDigest(turn.frame["model"]), nativeWireDigest(result.result.UpstreamModel))
			assert.Equal(t, "response.completed", result.result.UpstreamTerminalEvent)
		case <-ctx.Done():
			t.Fatal("missing native WS ingress turn completion")
		}
	}
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case err := <-serverErr:
		finished = true
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("native WS ingress did not finish")
	}
	wsCapture.mu.Lock()
	writes := append([]map[string]any(nil), wsCapture.writes...)
	wsCapture.mu.Unlock()
	if mode == OpenAIWSIngressModeCtxPool {
		require.Equal(t, 1, dialer.DialCount(), "native continuation must keep the upstream connection")
		require.Zero(t, len(httpCapture.requests))
		require.Equal(t, len(turns), len(writes))
	} else {
		require.Zero(t, dialer.DialCount())
		require.Zero(t, len(writes))
		require.Equal(t, len(turns), len(httpCapture.requests))
		require.Equal(t, len(turns), len(httpCapture.bodies))
	}
	for i, turn := range turns {
		want := nativeWSReplayClone(t, turn.frame)
		var got map[string]any
		if mode == OpenAIWSIngressModeCtxPool {
			got = writes[i]
		} else {
			require.NoError(t, json.Unmarshal(httpCapture.bodies[i], &got))
			// Only the oracle changes framing; the client always sends raw WS.
			delete(want, "type")
			delete(want, "generate")
			want["stream"] = true
			if turn.httpInput != nil {
				want["input"] = turn.httpInput
				delete(want, "previous_response_id")
			}
			metadata, _ := turn.frame["client_metadata"].(map[string]any)
			liteHeader := ""
			if metadata[nativeWSReplayLiteMarker] == "true" {
				liteHeader = "true"
			}
			assert.Equal(t, liteHeader, httpCapture.requests[i].Header.Get(responsesLiteHeader), "turn %d Lite adapter header", i+1)
			assert.Equal(t, http.MethodPost, httpCapture.requests[i].Method)
		}
		for _, field := range []string{
			"type", "generate", "stream", "model", "instructions", "input", "tools", "tool_choice", "reasoning",
			"include", "text", "parallel_tool_calls", "previous_response_id", "prompt_cache_key", "store",
			"max_output_tokens", "service_tier", "client_metadata",
		} {
			before, existsBefore := want[field]
			after, existsAfter := got[field]
			assert.Equal(t, existsBefore, existsAfter, "turn %d field presence changed: %s", i+1, field)
			assert.Equal(t, nativeWireDigest(before), nativeWireDigest(after), "turn %d field content changed: %s", i+1, field)
		}
		assert.Equal(t, nativeWireDigest(want), nativeWireDigest(got), "turn %d contains an unclassified request difference", i+1)
	}
}

func nativeWSReplayClone(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	var cloned map[string]any
	require.NoError(t, json.Unmarshal(data, &cloned))
	return cloned
}

func nativeWSReplayToolEvents(t *testing.T) (map[string]any, [][]byte) {
	t.Helper()
	call := map[string]any{
		"type": "function_call", "id": "fc_synthetic_audit", "call_id": "call_synthetic_audit",
		"name": "sleep", "namespace": "clock", "arguments": `{"duration_ms":1}`, "status": "completed",
	}
	response := map[string]any{
		"id": "resp_audit_tool", "model": "gpt-6-astra", "status": "completed", "output": []any{call},
		"usage": map[string]any{"input_tokens": 30, "output_tokens": 10},
	}
	var events [][]byte
	for _, event := range []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": response["id"], "model": response["model"], "status": "in_progress", "output": []any{}}},
		{"type": "response.function_call_arguments.delta", "item_id": call["id"], "output_index": 0, "delta": call["arguments"]},
		{"type": "response.function_call_arguments.done", "item_id": call["id"], "output_index": 0, "arguments": call["arguments"]},
		{"type": "response.output_item.done", "output_index": 0, "item": call},
		{"type": "response.completed", "response": response},
	} {
		data, err := json.Marshal(event)
		require.NoError(t, err)
		events = append(events, data)
	}
	return call, events
}
