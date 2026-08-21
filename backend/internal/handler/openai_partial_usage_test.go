package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// openAIHandlerPartialUsageUpstream returns a fresh response for every attempt
// so the same handler fixture can exercise all three OpenAI-compatible routes.
type openAIHandlerPartialUsageUpstream struct {
	body          string
	terminalError error
}

func (u *openAIHandlerPartialUsageUpstream) response() *http.Response {
	body := io.ReadCloser(io.NopCloser(strings.NewReader(u.body)))
	if u.terminalError != nil {
		body = &openAIHandlerPartialUsageReadCloser{
			Reader:        strings.NewReader(u.body),
			terminalError: u.terminalError,
		}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"rid-partial-handler"},
		},
		Body: body,
	}
}

func (u *openAIHandlerPartialUsageUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.response(), nil
}

func (u *openAIHandlerPartialUsageUpstream) DoWithTLS(_ *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.response(), nil
}

type openAIHandlerPartialUsageReadCloser struct {
	*strings.Reader
	terminalError error
}

func (r *openAIHandlerPartialUsageReadCloser) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		return n, r.terminalError
	}
	return n, err
}

func (r *openAIHandlerPartialUsageReadCloser) Close() error { return nil }

const openAIHandlerPartialUsageSSE = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-partial-handler\",\"model\":\"gpt-5.4\",\"status\":\"in_progress\",\"output\":[]}}\n\n" +
	"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
	"event: response.failed\n" +
	"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp-partial-handler\",\"object\":\"response\",\"model\":\"gpt-5.4\",\"status\":\"failed\",\"output\":[],\"usage\":{\"input_tokens\":11,\"output_tokens\":5,\"total_tokens\":16},\"error\":{\"code\":\"context_window_exceeded\",\"message\":\"input exceeds the context window\"}}}\n\n"

const openAIHandlerPartialUsageChatCompletionsSSE = "data: {\"id\":\"chatcmpl-partial-handler\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5.4\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n" +
	"data: {\"id\":\"chatcmpl-partial-handler\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5.4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n" +
	"data: {\"id\":\"chatcmpl-partial-handler\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5.4\",\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":5,\"total_tokens\":16}}\n\n"

func newOpenAIHandlerForPartialUsageTest(t *testing.T, responsesFallback bool) (*OpenAIGatewayHandler, <-chan *service.UsageLog) {
	t.Helper()

	account := service.Account{
		ID:          952,
		Name:        "openai-partial-usage-handler",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		Priority:    1,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "test-access-token",
			"chatgpt_account_id": "test-chatgpt-account",
		},
	}
	upstream := &openAIHandlerPartialUsageUpstream{body: openAIHandlerPartialUsageSSE}
	if responsesFallback {
		account.Type = service.AccountTypeAPIKey
		account.Extra = map[string]any{
			openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
		}
		account.Credentials = map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://upstream.example",
		}
		upstream.body = openAIHandlerPartialUsageChatCompletionsSSE
		upstream.terminalError = io.ErrUnexpectedEOF
	}
	accountRepo := &openAIWSUsageHandlerAccountRepoStub{account: account}
	usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 2)}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false

	billingCacheService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheService.Stop)
	gatewayService := service.NewOpenAIGatewayService(
		accountRepo,
		usageRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		cfg,
		nil,
		nil,
		service.NewBillingService(cfg, nil),
		nil,
		billingCacheService,
		upstream,
		&service.DeferredService{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	workerPool := newUsageRecordTestPool(t)
	cache := &concurrencyCacheMock{
		acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) {
			return true, nil
		},
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) {
			return true, nil
		},
	}
	h := NewOpenAIGatewayHandler(
		gatewayService,
		service.NewConcurrencyService(cache),
		billingCacheService,
		&service.APIKeyService{},
		workerPool,
		nil,
		nil,
		nil,
		cfg,
	)
	return h, usageRepo.created
}

func TestOpenAIHandlersRecordPartialUsageWhenForwardReturnsResultAndError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name              string
		path              string
		body              string
		responsesFallback bool
		invoke            func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{
			name: "chat completions",
			path: "/openai/v1/chat/completions",
			body: `{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`,
			invoke: func(h *OpenAIGatewayHandler, c *gin.Context) {
				h.ChatCompletions(c)
			},
		},
		{
			name: "native responses",
			path: "/openai/v1/responses",
			body: `{"model":"gpt-5.4","input":"hello","stream":true}`,
			invoke: func(h *OpenAIGatewayHandler, c *gin.Context) {
				h.Responses(c)
			},
		},
		{
			name:              "responses",
			path:              "/openai/v1/responses",
			body:              `{"model":"gpt-5.4","input":"hello","stream":true}`,
			responsesFallback: true,
			invoke: func(h *OpenAIGatewayHandler, c *gin.Context) {
				h.Responses(c)
			},
		},
		{
			name: "messages",
			path: "/openai/v1/messages",
			body: `{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":true}`,
			invoke: func(h *OpenAIGatewayHandler, c *gin.Context) {
				h.Messages(c)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, usageLogs := newOpenAIHandlerForPartialUsageTest(t, tc.responsesFallback)
			groupID := int64(951)
			apiKey := &service.APIKey{
				ID:      953,
				GroupID: &groupID,
				User:    &service.User{ID: 954, Status: service.StatusActive},
				Group: &service.Group{
					ID:                    groupID,
					Platform:              service.PlatformOpenAI,
					Status:                service.StatusActive,
					AllowMessagesDispatch: true,
				},
			}

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})

			tc.invoke(h, c)

			var usageLog *service.UsageLog
			select {
			case usageLog = <-usageLogs:
			case <-time.After(3 * time.Second):
				t.Fatalf("等待部分 usage 落账超时: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			require.NotNil(t, usageLog)
			require.Equal(t, 11, usageLog.InputTokens)
			require.Equal(t, 5, usageLog.OutputTokens)
			require.Equal(t, int64(952), usageLog.AccountID)

			select {
			case duplicate := <-usageLogs:
				t.Fatalf("部分 result 不应重复计费，收到第二条 usage log: %#v", duplicate)
			case <-time.After(150 * time.Millisecond):
			}
		})
	}
}
