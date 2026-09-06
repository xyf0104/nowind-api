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
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// This recorder stops at the outbound request; it never contacts an upstream.
func captureQualityAuditHTTPBody(t *testing.T, accountType string, passthrough bool, payload map[string]any, userAgent ...string) []byte {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.153.4")
	c.Request.Header.Set("originator", "codex_cli_rs")
	if len(userAgent) > 0 {
		c.Request.Header.Set("User-Agent", userAgent[0])
		c.Request.Header.Del("originator")
	}
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"audit capture complete"}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID: 771, Platform: PlatformOpenAI, Type: accountType, Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "audit-placeholder", "chatgpt_account_id": "audit-placeholder",
			"api_key": "audit-placeholder", "base_url": "https://example.com",
		},
		Extra: map[string]any{"openai_passthrough": passthrough, "use_responses_api": true},
	}
	_, err = svc.Forward(context.Background(), c, account, body)
	require.Error(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Len(t, upstream.bodies, 1)
	return upstream.lastBody
}

func TestOpenAIRequestFidelityHTTPAstraPreservesExplicitPayload(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, passthrough := range []bool{false, true} {
			for _, effort := range []string{"max", "ultra", "xhigh"} {
				mode := "normalized"
				if passthrough {
					mode = "passthrough"
				}
				t.Run(accountType+"/"+mode+"/"+effort, func(t *testing.T) {
					text := "HEAD " + strings.Repeat("long context <&> ", 20000) + " TAIL"
					out := captureQualityAuditHTTPBody(t, accountType, passthrough, map[string]any{
						"model": "gpt-6-astra", "stream": true,
						"instructions":    "EXACT CLIENT INSTRUCTIONS <&>",
						"reasoning":       map[string]any{"effort": effort, "summary": "auto"},
						"text":            map[string]any{"verbosity": "high"},
						"truncation":      "disabled",
						"input":           []any{map[string]any{"type": "message", "role": "user", "content": text}},
						"client_metadata": map[string]any{"audit_unknown": "preserve"},
					})
					require.Equal(t, "gpt-6-astra", gjson.GetBytes(out, "model").String())
					require.Equal(t, effort, gjson.GetBytes(out, "reasoning.effort").String())
					require.Equal(t, "auto", gjson.GetBytes(out, "reasoning.summary").String())
					require.Equal(t, "EXACT CLIENT INSTRUCTIONS <&>", gjson.GetBytes(out, "instructions").String())
					require.Equal(t, "high", gjson.GetBytes(out, "text.verbosity").String())
					require.Equal(t, "disabled", gjson.GetBytes(out, "truncation").String())
					require.Equal(t, text, gjson.GetBytes(out, "input.0.content").String())
					require.Equal(t, "preserve", gjson.GetBytes(out, "client_metadata.audit_unknown").String())
				})
			}
		}
	}
}

func TestOpenAIRequestFidelityEffortLoggingIsNotWireNormalization(t *testing.T) {
	for _, effort := range []string{"max", "ultra", "xhigh", "future_effort"} {
		t.Run(effort, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"` + effort + `"}}`)
			unchanged, changed := ApplyOpenAIReasoningEffortPolicy(body, "", nil)
			require.False(t, changed)
			require.Equal(t, body, unchanged)
			logged := extractOpenAIReasoningEffortFromBody(body, "gpt-6-astra")
			require.NotNil(t, logged)
			require.Equal(t, effort, *logged)
			capped, _ := ApplyOpenAIReasoningEffortPolicy(body, "high", nil)
			if effort == "ultra" || effort == "future_effort" {
				require.Equal(t, body, capped)
			} else {
				require.Equal(t, "high", gjson.GetBytes(capped, "reasoning.effort").String())
			}
		})
	}
}

func TestOpenAIRequestFidelityEffortLoggingBoundsMapAndBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"ultra", "ultra", "ultra"},
		{"known case alias", "HIGH", "high"},
		{"genuine alias", " X-High ", "xhigh"},
		{"extra high alias", "extra_high", "xhigh"},
		{"old model max stays max", "max", "max"},
		{"unknown identifier", "future_effort", "future_effort"},
		{"not a genuine alias", "m-a-x", "m-a-x"},
		{"invalid spaced mode", "m a x", ""},
		{"limit", strings.Repeat("a", 20), strings.Repeat("a", 20)},
		{"over limit", strings.Repeat("a", 21), ""},
		{"oversized known prefix", "max" + strings.Repeat("a", 4096), ""},
		{"invalid whitespace", "future effort", ""},
		{"invalid punctuation", "ultra<script>", ""},
		{"invalid control", "ultra\nmode", ""},
		{"non ascii", "\u00e9ffort", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, nested := range []bool{true, false} {
				payload := map[string]any{"model": "gpt-5.5-high"}
				if nested {
					payload["reasoning"] = map[string]any{"effort": tc.raw}
				} else {
					payload["reasoning_effort"] = tc.raw
				}
				body, err := json.Marshal(payload)
				require.NoError(t, err)
				for _, got := range []*string{
					extractOpenAIReasoningEffortFromBody(body, "gpt-5.5-high"),
					extractOpenAIReasoningEffort(payload, "gpt-5.5-high"),
				} {
					if tc.want == "" {
						require.Nil(t, got, "invalid explicit effort must not fall back to the model suffix")
					} else {
						require.NotNil(t, got)
						require.Equal(t, tc.want, *got)
						require.LessOrEqual(t, len(*got), 20)
					}
				}
				after, err := json.Marshal(payload)
				require.NoError(t, err)
				require.Equal(t, body, after)
			}
		})
	}
}

func TestOpenAIRequestFidelityEffortLoggingOmitsInvalidTypes(t *testing.T) {
	for _, raw := range []any{true, float64(42), []any{"max"}, map[string]any{"effort": "max"}} {
		for _, nested := range []bool{false, true} {
			payload := map[string]any{"model": "gpt-5.5-high"}
			if nested {
				payload["reasoning"] = map[string]any{"effort": raw}
			} else {
				payload["reasoning_effort"] = raw
			}
			body, err := json.Marshal(payload)
			require.NoError(t, err)
			require.Nil(t, extractOpenAIReasoningEffortFromBody(body, "gpt-5.5-high"))
			require.Nil(t, extractOpenAIReasoningEffort(payload, "gpt-5.5-high"))
		}
	}
}

func TestOpenAIRequestFidelityHermesHTTPPreservesExplicitEffort(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, passthrough := range []bool{false, true} {
			for _, effort := range []string{"max", "ultra", "xhigh"} {
				mode := "normalized"
				if passthrough {
					mode = "passthrough"
				}
				t.Run(accountType+"/"+mode+"/"+effort, func(t *testing.T) {
					out := captureQualityAuditHTTPBody(t, accountType, passthrough, map[string]any{
						"model": "gpt-6-astra", "stream": true, "input": "hello",
						"instructions": "HERMES CLIENT INSTRUCTIONS",
						"reasoning":    map[string]any{"effort": effort},
					}, "Mozilla/5.0 (Hermes/1.0)")
					require.Equal(t, "gpt-6-astra", gjson.GetBytes(out, "model").String())
					require.Equal(t, effort, gjson.GetBytes(out, "reasoning.effort").String())
					require.Equal(t, "HERMES CLIENT INSTRUCTIONS", gjson.GetBytes(out, "instructions").String())
				})
			}
		}
	}
}

// A compaction item may be the only copy of prior context.
func TestOpenAIRequestFidelityOAuthHTTPPreservesCompactedHistory(t *testing.T) {
	for _, tc := range []struct {
		name    string
		present bool
		value   any
	}{
		{name: "nonempty", present: true, value: "resp_previous"},
		{name: "empty", present: true, value: ""},
		{name: "null", present: true},
		{name: "absent_control"},
	} {
		payload := map[string]any{
			"model": "gpt-6-astra", "instructions": "Keep the prior context.",
			"input": []any{
				map[string]any{"type": "compaction_summary", "encrypted_content": "audit-opaque-context"},
				map[string]any{"type": "reasoning", "summary": []any{}, "encrypted_content": "audit-opaque-reasoning"},
				map[string]any{"type": "message", "role": "user", "content": "Continue."},
			},
		}
		if tc.present {
			payload["previous_response_id"] = tc.value
		}
		t.Run(tc.name, func(t *testing.T) {
			out := captureQualityAuditHTTPBody(t, AccountTypeOAuth, true, payload)
			require.Equal(t, "audit-opaque-context", gjson.GetBytes(out, "input.0.encrypted_content").String())
			require.Equal(t, "audit-opaque-reasoning", gjson.GetBytes(out, "input.1.encrypted_content").String())
			require.Equal(t, tc.present, gjson.GetBytes(out, "previous_response_id").Exists())
			if tc.value != nil {
				require.Equal(t, tc.value, gjson.GetBytes(out, "previous_response_id").String())
			}
			native := captureQualityAuditHTTPBody(t, AccountTypeOAuth, false, payload, "Mozilla/5.0 (Hermes/1.0)")
			require.Equal(t, "audit-opaque-context", gjson.GetBytes(native, "input.0.encrypted_content").String())
			require.Equal(t, "audit-opaque-reasoning", gjson.GetBytes(native, "input.1.encrypted_content").String())
		})
	}
}

// Platform Responses supports continuation; delta-only input
// cannot safely become a standalone request by deleting previous_response_id.
func TestOpenAIRequestFidelityAPIKeyHTTPPreservesContinuation(t *testing.T) {
	out := captureQualityAuditHTTPBody(t, AccountTypeAPIKey, false, map[string]any{
		"model": "gpt-6-astra", "instructions": "Client instructions.",
		"previous_response_id": "resp_previous", "input": "Continue the plan.",
	})
	require.Equal(t, "resp_previous", gjson.GetBytes(out, "previous_response_id").String())
}

// API key Responses does not require an injected Codex persona.
func TestOpenAIRequestFidelityAPIKeyHTTPDoesNotInventInstructions(t *testing.T) {
	out := captureQualityAuditHTTPBody(t, AccountTypeAPIKey, false, map[string]any{
		"model": "gpt-6-astra", "input": "Answer the question.",
	})
	require.False(t, gjson.GetBytes(out, "instructions").Exists(), "unsolicited instructions were injected")
}

func TestOpenAIRequestFidelityHTTPPassthroughLogsWireEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mappedModel := range []string{"gpt-6-astra", "gpt-5.5"} {
		for _, effort := range []string{"max", "ultra", "future_effort", strings.Repeat("a", 21)} {
			t.Run(mappedModel+"/"+effort, func(t *testing.T) {
				body, marshalErr := json.Marshal(map[string]any{
					"model": "gpt-6-astra", "reasoning": map[string]any{"effort": effort},
					"instructions": "Client instructions.", "input": "hello", "stream": true,
				})
				require.NoError(t, marshalErr)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader("data: " + `{"type":"response.completed","response":{"id":"resp_audit","model":"gpt-6-astra","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\ndata: [DONE]\n\n")),
				}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				account := &Account{
					ID: 771, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
					Credentials: map[string]any{
						"access_token": "audit-placeholder", "chatgpt_account_id": "audit-placeholder",
						"model_mapping": map[string]any{"gpt-6-astra": mappedModel},
					},
					Extra: map[string]any{"openai_passthrough": true},
				}
				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, effort, gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
				require.Equal(t, "gpt-6-astra", result.Model)
				require.Equal(t, "gpt-6-astra", result.UpstreamResponseModel)
				if len(effort) > 20 {
					require.Nil(t, result.ReasoningEffort, "oversized effort must not break usage insertion")
				} else {
					require.NotNil(t, result.ReasoningEffort)
					require.Equal(t, effort, *result.ReasoningEffort, "usage must describe the forwarded wire body, not an unused account mapping")
				}
			})
		}
	}
}

func TestOpenAIRequestFidelityInstructionsScope(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, instructions := range []any{nil, "", "  ", "client instructions"} {
			payload := map[string]any{"model": "gpt-6-astra", "input": "hello", "instructions": instructions}
			out := captureQualityAuditHTTPBody(t, accountType, false, payload, "Mozilla/5.0 (Hermes/1.0)")
			if accountType == AccountTypeOAuth && instructions != "client instructions" {
				require.Equal(t, defaultCodexSynthInstructions("gpt-6-astra"), gjson.GetBytes(out, "instructions").String())
			} else {
				expected, err := json.Marshal(instructions)
				require.NoError(t, err)
				require.JSONEq(t, string(expected), gjson.GetBytes(out, "instructions").Raw)
			}
		}
	}
}

func TestOpenAIRequestFidelityEmptyAnchorRetryIsExact(t *testing.T) {
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, tc := range []struct {
		name     string
		account  *Account
		status   int
		anchor   string
		response string
		want     bool
	}{
		{"empty unsupported", oauth, 400, `""`, `{"error":{"code":"unsupported_parameter","param":"previous_response_id"}}`, true},
		{"null unknown", oauth, 400, `null`, `{"error":{"code":"unknown_parameter","param":"previous_response_id"}}`, true},
		{"real chain", oauth, 400, `"resp_pinned"`, `{"error":{"code":"unsupported_parameter","param":"previous_response_id"}}`, false},
		{"wrong parameter", oauth, 400, `null`, `{"error":{"code":"unsupported_parameter","param":"input"}}`, false},
		{"wrong status", oauth, 429, `null`, `{"error":{"code":"unsupported_parameter","param":"previous_response_id"}}`, false},
		{"other error", oauth, 400, `null`, `{"error":{"code":"invalid_encrypted_content","param":"previous_response_id"}}`, false},
		{"message only", oauth, 400, `null`, `{"error":{"message":"Unsupported parameter: previous_response_id"}}`, false},
		{"invalid type", oauth, 400, `false`, `{"error":{"code":"unsupported_parameter","param":"previous_response_id"}}`, false},
		{"API key", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, 400, `null`, `{"error":{"code":"unsupported_parameter","param":"previous_response_id"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"previous_response_id":` + tc.anchor + `,"input":[{"type":"compaction","encrypted_content":"opaque"},{"type":"reasoning","encrypted_content":"reasoning","summary":[]}]}`)
			out, changed, err := normalizeOpenAIOAuthEmptyPreviousResponseIDRetryBody(tc.account, tc.status, body, []byte(tc.response))
			require.NoError(t, err)
			require.Equal(t, tc.want, changed)
			require.Equal(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(out, "input").Raw)
			if changed {
				require.False(t, gjson.GetBytes(out, "previous_response_id").Exists())
				_, retriedAgain, err := normalizeOpenAIOAuthEmptyPreviousResponseIDRetryBody(tc.account, tc.status, out, []byte(tc.response))
				require.NoError(t, err)
				require.False(t, retriedAgain)
			} else {
				require.Equal(t, body, out)
			}
		})
	}
}

func TestOpenAIRequestFidelityOAuthRejectedAnchorPreservesHistoryAndBinding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		anchor   any
		requests int
		wantErr  bool
	}{
		{"empty retries once", "", 2, false},
		{"null retries once", nil, 2, false},
		{"real chain is not replayed", "resp_pinned", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"model": "gpt-6-astra", "instructions": "client instructions", "previous_response_id": tc.anchor,
				"input": []any{map[string]any{"type": "compaction_summary", "encrypted_content": "opaque-history"}},
			})
			require.NoError(t, err)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			cache := &stubGatewayCache{}
			store := NewOpenAIWSStateStore(cache)
			require.NoError(t, store.BindResponseAccount(context.Background(), 0, "resp_pinned", 771, time.Hour))
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				{StatusCode: 400, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"unsupported_parameter","param":"previous_response_id","message":"Unsupported parameter: previous_response_id"}}`))},
				{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"model":"gpt-6-astra","usage":{"input_tokens":1,"output_tokens":1}}`))},
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, cache: cache, openaiWSStateStore: store}
			account := &Account{ID: 771, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
				Credentials: map[string]any{"access_token": "audit-placeholder", "chatgpt_account_id": "audit-placeholder"},
				Extra:       map[string]any{"openai_passthrough": true},
			}
			_, err = svc.Forward(context.Background(), c, account, body)
			if tc.wantErr {
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.NotErrorAs(t, err, &failover)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, upstream.bodies, tc.requests)
			for _, out := range upstream.bodies {
				require.JSONEq(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(out, "input").Raw)
			}
			if tc.wantErr {
				require.Equal(t, "resp_pinned", gjson.GetBytes(upstream.lastBody, "previous_response_id").String())
			}
			boundID, err := store.GetResponseAccount(context.Background(), 0, "resp_pinned")
			require.NoError(t, err)
			require.Equal(t, int64(771), boundID)
		})
	}
}

func TestOpenAIRequestFidelityWSPassthroughPreservesAstraTurns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		t.Run(accountType, func(t *testing.T) {
			controlCtx, cancel := context.WithCancelCause(context.Background())
			defer cancel(context.Canceled)
			upstream := newStagedPassthroughConn()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			account := passthroughLifecycleAccount()
			account.Type = accountType
			account.Credentials["chatgpt_account_id"] = "audit-placeholder"
			account.Extra["openai_oauth_responses_websockets_v2_mode"] = OpenAIWSIngressModePassthrough
			server, serverErr := startPassthroughLifecycleServer(t, controlCtx, newPassthroughLifecycleService(cfg, upstream), account)
			defer server.Close()
			var client *coderws.Conn
			for turn, effort := range []string{"max", "ultra", "xhigh"} {
				payload, err := json.Marshal(map[string]any{
					"type": "response.create", "model": "gpt-6-astra", "instructions": "CLIENT ASTRA INSTRUCTIONS",
					"reasoning":            map[string]any{"effort": effort, "summary": "auto"},
					"previous_response_id": "resp_history",
					"input":                []any{map[string]any{"type": "compaction_summary", "encrypted_content": "audit-opaque-context"}},
					"tools":                []any{map[string]any{"type": "namespace", "name": "collaboration", "tools": []any{map[string]any{"type": "function", "name": "spawn_agent"}}}},
				})
				require.NoError(t, err)
				if turn == 0 {
					client = dialPassthroughLifecycleClientWithPayload(t, server, string(payload))
					defer func() { _ = client.CloseNow() }()
				} else {
					writeCtx, cancelWrite := context.WithTimeout(controlCtx, 3*time.Second)
					err = client.Write(writeCtx, coderws.MessageText, payload)
					cancelWrite()
					require.NoError(t, err)
				}
				out := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
				require.Equal(t, "gpt-6-astra", gjson.GetBytes(out, "model").String())
				require.Equal(t, effort, gjson.GetBytes(out, "reasoning.effort").String())
				require.Equal(t, "CLIENT ASTRA INSTRUCTIONS", gjson.GetBytes(out, "instructions").String())
				require.Equal(t, "resp_history", gjson.GetBytes(out, "previous_response_id").String())
				require.Equal(t, "audit-opaque-context", gjson.GetBytes(out, "input.0.encrypted_content").String())
				require.Equal(t, "collaboration", gjson.GetBytes(out, "tools.0.name").String())
				require.Equal(t, "spawn_agent", gjson.GetBytes(out, "tools.0.tools.0.name").String())
				upstream.Send(`{"type":"response.completed","response":{"id":"resp_audit","model":"gpt-6-astra","usage":{"input_tokens":1,"output_tokens":1}}}`)
				event, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
				require.NoError(t, err)
				require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
			}
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case err := <-serverErr:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("WS audit did not finish")
			}
		})
	}
}
