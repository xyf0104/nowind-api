package service

import (
	"context"
	"errors"
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

func TestProxyResponsesWebSocketFromClientMarksCyberPolicyBeforeEarlyReturn(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		upstreamEvent []byte
		wantInput     int
		wantOutput    int
	}{
		{
			name:          "error_rate_limit_type_stops_on_exact_cyber_code",
			upstreamEvent: []byte(`{"type":"error","error":{"type":"rate_limit_error","code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":5,"output_tokens":1}}`),
			wantInput:     5,
			wantOutput:    1,
		},
		{
			name:          "response_failed_terminal",
			upstreamEvent: []byte(`{"type":"response.failed","response":{"id":"resp_cyber","status":"failed","error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":9,"output_tokens":2}}}`),
			wantInput:     9,
			wantOutput:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newOpenAIWSV2TestConfig()
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
			cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0

			captureConn := &openAIWSCaptureConn{events: [][]byte{append([]byte(nil), tt.upstreamEvent...)}}
			pool := newOpenAIWSConnPool(cfg)
			captureDialer := &openAIWSCaptureDialer{conn: captureConn}
			pool.setClientDialerForTest(captureDialer)
			svc := &OpenAIGatewayService{
				cfg:              cfg,
				httpUpstream:     &httpUpstreamRecorder{},
				cache:            &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
				openaiWSPool:     pool,
			}
			account := &Account{
				ID: 5402, Name: "openai-ingress-cyber", Platform: PlatformOpenAI,
				Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool,
				},
			}

			markCh := make(chan *CyberPolicyMark, 1)
			serverErrCh := make(chan error, 1)
			wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
				if err != nil {
					serverErrCh <- err
					return
				}
				defer func() { _ = conn.CloseNow() }()

				readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
				_, firstMessage, err := conn.Read(readCtx)
				cancelRead()
				if err != nil {
					serverErrCh <- err
					return
				}

				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = r.Clone(r.Context())
				hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, _ *OpenAIForwardResult, _ error) {
					markCh <- GetOpsCyberPolicy(c)
				}}
				serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "sk-test", firstMessage, hooks)
			}))
			defer wsServer.Close()

			dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
			clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
			cancelDial()
			require.NoError(t, err)
			defer func() { _ = clientConn.CloseNow() }()

			writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
			err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","stream":false}`))
			cancelWrite()
			require.NoError(t, err)

			readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
			_, message, readErr := clientConn.Read(readCtx)
			cancelRead()
			require.NoError(t, readErr)
			require.Equal(t, "evt_content_moderation_blocked", gjson.GetBytes(message, "event_id").String())
			require.Equal(t, "error", gjson.GetBytes(message, "type").String())
			require.Equal(t, "invalid_request_error", gjson.GetBytes(message, "error.type").String())
			require.Equal(t, "content_policy_violation", gjson.GetBytes(message, "error.code").String())
			require.Equal(t, OpenAIWSCyberPolicyClientMessage, gjson.GetBytes(message, "error.message").String())

			select {
			case mark := <-markCh:
				require.NotNil(t, mark)
				require.Equal(t, tt.wantInput, mark.UpstreamInTok)
				require.Equal(t, tt.wantOutput, mark.UpstreamOutTok)
			case <-time.After(3 * time.Second):
				t.Fatal("AfterTurn did not observe the cyber mark")
			}

			select {
			case serverErr := <-serverErrCh:
				require.ErrorIs(t, serverErr, ErrOpenAIWSCyberPolicyBlocked)
				var failoverErr *UpstreamFailoverError
				require.False(t, errors.As(serverErr, &failoverErr))
			case <-time.After(5 * time.Second):
				t.Fatal("waiting for ingress websocket shutdown timed out")
			}

			captureConn.mu.Lock()
			upstreamWrites := len(captureConn.writes)
			captureConn.mu.Unlock()
			require.Equal(t, 1, upstreamWrites, "cyber policy must not retry the same account")
			require.Equal(t, 1, captureDialer.DialCount(), "cyber policy must not reconnect the same account")

			secondReadCtx, cancelSecondRead := context.WithTimeout(context.Background(), 3*time.Second)
			_, _, secondReadErr := clientConn.Read(secondReadCtx)
			cancelSecondRead()
			require.Error(t, secondReadErr, "cyber policy must emit exactly one client error frame")
			select {
			case <-markCh:
				t.Fatal("cyber policy must invoke AfterTurn exactly once")
			default:
			}
		})
	}
}

func TestProxyResponsesWebSocketFromClientPassthroughCyberPolicyStopsRawFrameAndSideEffects(t *testing.T) {
	gin.SetMode(gin.TestMode)

	controlCtx, cancelControl := context.WithCancel(context.Background())
	defer cancelControl()
	upstream := newStagedPassthroughConn()
	upstream.Send(`{"type":"error","error":{"type":"rate_limit_error","code":"cyber_policy","message":"blocked raw passthrough payload"},"usage":{"input_tokens":7,"output_tokens":3}}`)
	upstream.Send(`{"type":"response.failed","response":{"error":{"code":"cyber_policy","message":"duplicate raw payload"}}}`)

	cfg := passthroughLifecycleConfig()
	rateLimitRepo := &openAIWSRateLimitSignalRepo{}
	svc := newPassthroughLifecycleService(cfg, upstream)
	svc.rateLimitService = &RateLimitService{accountRepo: rateLimitRepo}
	account := passthroughLifecycleAccount()

	type afterTurnObservation struct {
		mark *CyberPolicyMark
		err  error
	}
	afterTurnCh := make(chan afterTurnObservation, 2)
	serverErrCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(controlCtx, 3*time.Second)
		msgType, firstMessage, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		if msgType != coderws.MessageText {
			serverErrCh <- errors.New("first message was not text")
			return
		}

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r.Clone(controlCtx)
		hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, _ *OpenAIForwardResult, turnErr error) {
			afterTurnCh <- afterTurnObservation{mark: GetOpsCyberPolicy(c), err: turnErr}
		}}
		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(controlCtx, c, conn, account, "sk-test", firstMessage, hooks)
	}))
	defer server.Close()

	clientConn := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = clientConn.CloseNow() }()

	message, readErr := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, readErr)
	require.Equal(t, "error", gjson.GetBytes(message, "type").String())
	require.Equal(t, OpenAIWSCyberPolicyClientMessage, gjson.GetBytes(message, "error.message").String())
	require.NotContains(t, string(message), "blocked raw passthrough payload")

	select {
	case observation := <-afterTurnCh:
		require.ErrorIs(t, observation.err, ErrOpenAIWSCyberPolicyBlocked)
		require.NotNil(t, observation.mark)
		require.Equal(t, 7, observation.mark.UpstreamInTok)
		require.Equal(t, 3, observation.mark.UpstreamOutTok)
	case <-time.After(3 * time.Second):
		t.Fatal("passthrough AfterTurn did not observe the cyber policy result")
	}

	select {
	case serverErr := <-serverErrCh:
		require.ErrorIs(t, serverErr, ErrOpenAIWSCyberPolicyBlocked)
		var failoverErr *UpstreamFailoverError
		require.False(t, errors.As(serverErr, &failoverErr))
	case <-time.After(3 * time.Second):
		t.Fatal("passthrough cyber policy relay did not stop")
	}

	_, secondReadErr := readPassthroughLifecycleFrame(t, clientConn, time.Second)
	require.Error(t, secondReadErr, "passthrough cyber policy must emit exactly one client frame")
	select {
	case <-afterTurnCh:
		t.Fatal("passthrough cyber policy must invoke AfterTurn exactly once")
	default:
	}
	require.Empty(t, rateLimitRepo.rateLimitCalls)
	require.Empty(t, rateLimitRepo.updateExtra)
	require.Equal(t, StatusActive, account.Status)
	require.Nil(t, account.RateLimitResetAt)
	require.Nil(t, account.TempUnschedulableUntil)
}

func TestProxyResponsesWebSocketFromClientInvalidEncryptedLineageStripsNextTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	upstreamConn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"error","error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"invalid ciphertext"}}`),
		[]byte(`{"type":"response.failed","response":{"id":"resp_lineage_1","error":{"code":"invalid_encrypted_content","message":"invalid ciphertext"}}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_lineage_2","usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSQueueDialer{conns: []openAIWSClientConn{upstreamConn}})
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}
	account := &Account{
		ID: 119, Name: "openai-lineage", Platform: PlatformOpenAI,
		Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
	}

	serverErrCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r.Clone(r.Context())
		c.Request.Header = c.Request.Header.Clone()
		c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		msgType, firstMessage, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			serverErrCh <- errors.New("unsupported websocket client message type")
			return
		}
		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "sk-test", firstMessage, nil)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeMessage := func(payload string) {
		writeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, clientConn.Write(writeCtx, coderws.MessageText, []byte(payload)))
	}
	readMessage := func() []byte {
		readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		msgType, message, readErr := clientConn.Read(readCtx)
		require.NoError(t, readErr)
		require.Equal(t, coderws.MessageText, msgType)
		return message
	}

	writeMessage(`{"type":"response.create","model":"gpt-5.1","stream":false,"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"stale-cipher","summary":[]},{"type":"input_text","text":"hi"}]}`)
	require.Equal(t, "error", gjson.GetBytes(readMessage(), "type").String())
	require.Equal(t, "response.failed", gjson.GetBytes(readMessage(), "type").String())

	writeMessage(`{"type":"response.create","model":"gpt-5.1","stream":false,"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"stale-cipher","summary":[]},{"type":"input_text","text":"hi"},{"type":"input_text","text":"again"}]}`)
	require.Equal(t, "response.completed", gjson.GetBytes(readMessage(), "type").String())
	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))

	select {
	case serverErr := <-serverErrCh:
		require.NoError(t, serverErr)
	case <-time.After(5 * time.Second):
		t.Fatal("waiting for ingress websocket shutdown timed out")
	}

	upstreamConn.mu.Lock()
	writes := append([]map[string]any(nil), upstreamConn.writes...)
	upstreamConn.mu.Unlock()
	require.Len(t, writes, 2)
	firstUpstream := requestToJSONString(writes[0])
	require.Equal(t, "stale-cipher", gjson.Get(firstUpstream, "input.0.encrypted_content").String())
	secondUpstream := requestToJSONString(writes[1])
	require.False(t, gjson.Get(secondUpstream, "input.0.encrypted_content").Exists())
	require.Equal(t, "rs_1", gjson.Get(secondUpstream, "input.0.id").String())
	require.Equal(t, "again", gjson.Get(secondUpstream, "input.2.text").String())
}
