package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func compactionGuardBodies() map[string][]byte {
	out := map[string][]byte{}
	for _, kind := range []string{"compaction", "compaction_summary"} {
		item := fmt.Sprintf(`{"type":%q,"id":"cmp_history","encrypted_content":"opaque-history","summary":[{"type":"summary_text","text":"not proof of full history"}]}`, kind)
		for _, shape := range []string{"only", "with_current_user", "object"} {
			input := "[" + item + "]"
			if shape == "with_current_user" {
				input = "[" + item + `,{"type":"message","role":"user","content":"continue","nonce":9007199254740993}]`
			}
			if shape == "object" {
				input = item
			}
			out[kind+"/"+shape] = []byte(`{"model":"gpt-6-astra","stream":false,"instructions":"client instructions","previous_response_id":"resp_pinned","input":` + input + `}`)
		}
	}
	return out
}

func compactionGuardErrorResponse() *http.Response {
	return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_encrypted_content","message":"bad encrypted content"}}`))}
}

func compactionGuardService(t *testing.T) (*OpenAIGatewayService, *Account, *httpUpstreamRecorder) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cache := &stubGatewayCache{}
	store := NewOpenAIWSStateStore(cache)
	require.NoError(t, store.BindResponseAccount(context.Background(), 0, "resp_pinned", 771, time.Hour))
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		compactionGuardErrorResponse(),
		{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"must_not_succeed","model":"gpt-6-astra","usage":{"input_tokens":1,"output_tokens":1}}`))},
	}}
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, cache: cache, openaiWSStateStore: store,
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector()}
	account := &Account{ID: 771, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-placeholder", "access_token": "test-placeholder", "base_url": "https://example.com"},
		Extra:       map[string]any{"use_responses_api": true, "responses_websockets_v2_enabled": true}}
	return svc, account, upstream
}

func requireCompactionGuardPinned(t *testing.T, svc *OpenAIGatewayService) {
	t.Helper()
	bound, err := svc.openaiWSStateStore.GetResponseAccount(context.Background(), 0, "resp_pinned")
	require.NoError(t, err)
	require.Equal(t, int64(771), bound)
}

func TestOpenAICompactionGuardHTTPNoReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, body := range compactionGuardBodies() {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, passthrough := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/passthrough=%v", name, accountType, passthrough), func(t *testing.T) {
					svc, account, upstream := compactionGuardService(t)
					account.Type = accountType
					account.Extra["openai_passthrough"] = passthrough
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
					SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
					result, err := svc.Forward(context.Background(), c, account, body)
					require.ErrorIs(t, err, ErrOpenAIContextUnavailable)
					var failover *UpstreamFailoverError
					require.NotErrorAs(t, err, &failover)
					require.Nil(t, result)
					require.Len(t, upstream.bodies, 1, "no shorter second request may leave the gateway")
					require.Equal(t, "resp_pinned", gjson.GetBytes(upstream.lastBody, "previous_response_id").String())
					expectedInput := gjson.GetBytes(body, "input").Raw
					if accountType == AccountTypeOAuth && passthrough && gjson.GetBytes(body, "input").IsObject() {
						expectedInput = "[" + expectedInput + "]"
					}
					require.JSONEq(t, expectedInput, gjson.GetBytes(upstream.lastBody, "input").Raw)
					require.Equal(t, http.StatusBadRequest, rec.Code)
					require.Equal(t, OpenAIContextUnavailableCode, gjson.Get(rec.Body.String(), "error.code").String())
					requireCompactionGuardPinned(t, svc)
				})
			}
		}
	}
}

func TestOpenAICompactionGuardWSNoReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, body := range compactionGuardBodies() {
		for _, event := range []string{
			`{"type":"error","error":{"code":"invalid_encrypted_content","message":"bad encrypted content"}}`,
			`{"type":"response.failed","response":{"error":{"code":"invalid_encrypted_content","message":"bad encrypted content"}}}`,
		} {
			t.Run(name+"/"+gjson.Get(event, "type").String(), func(t *testing.T) {
				svc, account, upstream := compactionGuardService(t)
				conn := &openAIWSCaptureConn{events: [][]byte{[]byte(event)}}
				dialer := &openAIWSCaptureDialer{conn: conn}
				pool := newOpenAIWSConnPool(svc.cfg)
				pool.setClientDialerForTest(dialer)
				svc.openaiWSPool = pool
				defer pool.Close()
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
				result, err := svc.Forward(context.Background(), c, account, body)
				require.ErrorIs(t, err, ErrOpenAIContextUnavailable)
				var failover *UpstreamFailoverError
				require.NotErrorAs(t, err, &failover)
				require.Nil(t, result)
				require.Empty(t, upstream.bodies, "WS must not fall back to HTTP")
				require.Equal(t, 1, dialer.DialCount())
				conn.mu.Lock()
				writes := append([]map[string]any(nil), conn.writes...)
				conn.mu.Unlock()
				require.Len(t, writes, 1)
				require.Equal(t, "resp_pinned", writes[0]["previous_response_id"])
				out, err := json.Marshal(writes[0]["input"])
				require.NoError(t, err)
				require.JSONEq(t, gjson.GetBytes(body, "input").Raw, string(out))
				require.Equal(t, OpenAIContextUnavailableCode, gjson.Get(rec.Body.String(), "error.code").String())
				requireCompactionGuardPinned(t, svc)
			})
		}
	}
}

func TestOpenAICompactionGuardSanitizersAreAtomic(t *testing.T) {
	invalid := map[string]struct{}{openAIEncryptedContentDigest("opaque-history"): {}}
	for name, body := range compactionGuardBodies() {
		t.Run(name, func(t *testing.T) {
			var decoded map[string]any
			require.NoError(t, decodeOpenAIJSONUseNumber(body, &decoded))
			before, err := json.Marshal(decoded)
			require.NoError(t, err)
			require.False(t, trimOpenAIEncryptedReasoningItems(decoded))
			require.False(t, dropOpenAIEncryptedReasoningInputItems(decoded))
			after, err := json.Marshal(decoded)
			require.NoError(t, err)
			require.Equal(t, before, after)
			for _, sanitize := range []func([]byte) ([]byte, bool, error){SanitizeOpenAICrossModeFailoverReasoning, trimGrokInvalidEncryptedContentRetryBody} {
				got, changed, err := sanitize(body)
				require.ErrorIs(t, err, ErrOpenAIContextUnavailable)
				require.False(t, changed)
				require.Equal(t, body, got)
			}
			got, count, err := stripOpenAIInvalidEncryptedContentRaw(body, invalid)
			require.ErrorIs(t, err, ErrOpenAIContextUnavailable)
			require.Zero(t, count)
			require.Equal(t, body, got)
			items, _, err := openAIWSExtractNormalizedInputSequence(body)
			require.NoError(t, err)
			require.True(t, openAIReplayHasInvalidEncryptedCompaction(items, invalid))
			next, count := stripOpenAIInvalidEncryptedContentFromReplayItems(items, invalid)
			require.Zero(t, count)
			require.Equal(t, items, next)
		})
	}
}

func TestOpenAICompactionGuardLineageStopsBeforeHTTP(t *testing.T) {
	for name, body := range compactionGuardBodies() {
		t.Run(name, func(t *testing.T) {
			svc, account, upstream := compactionGuardService(t)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			c.Set(openAIWSIngressSessionHashContextKey, "guard-session")
			svc.markOpenAIWSInvalidEncryptedContentLineage(0, account.ID, "guard-session", []string{openAIEncryptedContentDigest("opaque-history")})
			_, err := svc.Forward(context.Background(), c, account, body)
			require.ErrorIs(t, err, ErrOpenAIContextUnavailable)
			require.Empty(t, upstream.bodies)
			requireCompactionGuardPinned(t, svc)
		})
	}
}

func TestOpenAICompactionGuardWSIngressNoReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, body := range compactionGuardBodies() {
		for _, mode := range []string{"pooled", "pooled_known_invalid", "http_bridge", "http_bridge_sse", "http_bridge_known_invalid"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				svc, account, upstream := compactionGuardService(t)
				bridge := strings.HasPrefix(mode, "http_bridge")
				knownInvalid := strings.HasSuffix(mode, "known_invalid")
				if bridge {
					svc.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
					account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeHTTPBridge
				}
				if mode == "http_bridge_sse" {
					upstream.responses[0] = &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}},
						Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"invalid_encrypted_content\",\"message\":\"bad encrypted content\"}}}\n\n"))}
				}
				conn := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"error","error":{"code":"invalid_encrypted_content","message":"bad encrypted content"}}`)}}
				dialer := &openAIWSCaptureDialer{conn: conn}
				pool := newOpenAIWSConnPool(svc.cfg)
				pool.setClientDialerForTest(dialer)
				svc.openaiWSPool = pool
				defer pool.Close()
				serverErr := make(chan error, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					client, err := coderws.Accept(w, r, nil)
					if err != nil {
						serverErr <- err
						return
					}
					defer func() { _ = client.CloseNow() }()
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = r
					if knownInvalid {
						sessionHash := svc.GenerateSessionHash(c, body)
						svc.markOpenAIWSInvalidEncryptedContentLineage(0, account.ID, sessionHash, []string{openAIEncryptedContentDigest("opaque-history")})
					}
					serverErr <- svc.ProxyResponsesWebSocketFromClient(r.Context(), c, client, account, "test-placeholder", body, nil)
				}))
				defer server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
				require.NoError(t, err)
				defer func() { _ = client.CloseNow() }()
				_, frame, err := client.Read(ctx)
				require.NoError(t, err)
				require.Equal(t, OpenAIContextUnavailableCode, gjson.GetBytes(frame, "error.code").String())
				select {
				case err := <-serverErr:
					require.ErrorIs(t, err, ErrOpenAIContextUnavailable)
				case <-ctx.Done():
					t.Fatal("ingress did not terminate")
				}
				conn.mu.Lock()
				writes := append([]map[string]any(nil), conn.writes...)
				conn.mu.Unlock()
				if knownInvalid {
					require.Empty(t, upstream.bodies)
					require.Empty(t, writes)
				} else if bridge {
					require.Len(t, upstream.bodies, 1)
					require.Empty(t, writes)
					require.Equal(t, "resp_pinned", gjson.GetBytes(upstream.lastBody, "previous_response_id").String())
					require.True(t, hasOpenAIEncryptedCompactionRaw(upstream.lastBody))
				} else {
					require.Empty(t, upstream.bodies)
					require.Equal(t, 1, dialer.DialCount())
					require.Len(t, writes, 1)
					require.Equal(t, "resp_pinned", writes[0]["previous_response_id"])
				}
				requireCompactionGuardPinned(t, svc)
			})
		}
	}
}

func TestOpenAICompactionGuardMixedInputNeverPartiallyMutates(t *testing.T) {
	for _, kind := range []string{"compaction", "compaction_summary"} {
		for _, typed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/typed=%v", kind, typed), func(t *testing.T) {
				items := []map[string]any{
					{"type": "reasoning", "encrypted_content": "reasoning-cipher", "summary": []any{}},
					{"type": kind, "encrypted_content": "opaque-history"},
					{"type": "message", "role": "user", "content": "continue"},
				}
				body := map[string]any{"input": items, "previous_response_id": "resp_pinned"}
				if !typed {
					body["input"] = []any{items[0], items[1], items[2]}
				}
				before, err := json.Marshal(body)
				require.NoError(t, err)
				require.False(t, trimOpenAIEncryptedReasoningItems(body))
				require.False(t, dropOpenAIEncryptedReasoningInputItems(body))
				after, err := json.Marshal(body)
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
		}
	}
}

func TestOpenAICompactionGuardWSStreamingErrorNoReplay(t *testing.T) {
	for _, eventType := range []string{"error", "response.failed"} {
		t.Run(eventType, func(t *testing.T) {
			svc, account, upstream := compactionGuardService(t)
			event := `{"type":"error","error":{"code":"invalid_encrypted_content","message":"bad encrypted content"}}`
			if eventType == "response.failed" {
				event = `{"type":"response.failed","response":{"error":{"code":"invalid_encrypted_content","message":"bad encrypted content"}}}`
			}
			conn := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.created","response":{"id":"resp_failed","model":"gpt-6-astra"}}`),
				[]byte(event),
			}}
			dialer := &openAIWSCaptureDialer{conn: conn}
			pool := newOpenAIWSConnPool(svc.cfg)
			pool.setClientDialerForTest(dialer)
			svc.openaiWSPool = pool
			defer pool.Close()
			body := []byte(`{"model":"gpt-6-astra","stream":true,"previous_response_id":"resp_pinned","input":[{"type":"compaction","encrypted_content":"opaque-history"},{"type":"message","role":"user","content":"continue"}]}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			_, err := svc.Forward(context.Background(), c, account, body)
			require.ErrorIs(t, err, ErrOpenAIContextUnavailable)
			require.Contains(t, rec.Body.String(), OpenAIContextUnavailableCode)
			require.NotContains(t, rec.Body.String(), "response.completed")
			require.Empty(t, upstream.bodies)
			require.Equal(t, 1, dialer.DialCount())
			conn.mu.Lock()
			writes := append([]map[string]any(nil), conn.writes...)
			conn.mu.Unlock()
			require.Len(t, writes, 1)
			require.Equal(t, "resp_pinned", writes[0]["previous_response_id"])
			requireCompactionGuardPinned(t, svc)
		})
	}
}
