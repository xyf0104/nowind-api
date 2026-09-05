package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountManagementAccessSpy struct {
	service.AdminService
	denied map[int64]error
	calls  []int64
}

func (s *accountManagementAccessSpy) CheckAccountManagementAccess(_ context.Context, accountID int64) error {
	s.calls = append(s.calls, accountID)
	return s.denied[accountID]
}

type blockedOpenAIQuotaSpy struct {
	queryCalls int
	cacheCalls int
	resetCalls int
}

func (s *blockedOpenAIQuotaSpy) QueryUsage(context.Context, int64) (*service.OpenAIQuotaUsage, error) {
	s.queryCalls++
	return &service.OpenAIQuotaUsage{}, nil
}

func (s *blockedOpenAIQuotaSpy) CacheResetCreditsSnapshot(context.Context, int64, *service.OpenAIRateLimitResetCredits) error {
	s.cacheCalls++
	return nil
}

func (s *blockedOpenAIQuotaSpy) CachePostResetSnapshot(context.Context, int64, *service.OpenAIQuotaUsage) error {
	s.cacheCalls++
	return nil
}

func (s *blockedOpenAIQuotaSpy) ResetCredit(context.Context, int64) (*service.OpenAIQuotaResetResult, error) {
	s.resetCalls++
	return &service.OpenAIQuotaResetResult{}, nil
}

func remoteAccountReadOnlyError() error {
	return infraerrors.New(http.StatusForbidden, "ACCOUNT_REMOTE_NODE_READ_ONLY", "remote account is read-only")
}

func performAccountManagementRequest(t *testing.T, method, route, target, body string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	for _, item := range middleware {
		router.Use(item)
	}
	router.Handle(method, route, handler)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAccountSideEffectHandlersRejectRemoteAccountBeforeDownstream(t *testing.T) {
	tests := []struct {
		name   string
		method string
		route  string
		target string
		body   string
		bind   func(*AccountHandler) gin.HandlerFunc
	}{
		{name: "connectivity test", method: http.MethodPost, route: "/accounts/:id/test", target: "/accounts/42/test", body: `{}`, bind: func(h *AccountHandler) gin.HandlerFunc { return h.Test }},
		{name: "recover state", method: http.MethodPost, route: "/accounts/:id/recover-state", target: "/accounts/42/recover-state", bind: func(h *AccountHandler) gin.HandlerFunc { return h.RecoverState }},
		{name: "apply oauth credentials", method: http.MethodPost, route: "/accounts/:id/apply-oauth-credentials", target: "/accounts/42/apply-oauth-credentials", body: `{"type":"oauth","credentials":{"access_token":"new"}}`, bind: func(h *AccountHandler) gin.HandlerFunc { return h.ApplyOAuthCredentials }},
		{name: "clear error", method: http.MethodPost, route: "/accounts/:id/clear-error", target: "/accounts/42/clear-error", bind: func(h *AccountHandler) gin.HandlerFunc { return h.ClearError }},
		{name: "revert proxy fallback", method: http.MethodPost, route: "/accounts/:id/revert-proxy-fallback", target: "/accounts/42/revert-proxy-fallback", bind: func(h *AccountHandler) gin.HandlerFunc { return h.RevertProxyFallback }},
		{name: "reset account quota", method: http.MethodPost, route: "/accounts/:id/reset-quota", target: "/accounts/42/reset-quota", bind: func(h *AccountHandler) gin.HandlerFunc { return h.ResetQuota }},
		{name: "set schedulable", method: http.MethodPost, route: "/accounts/:id/schedulable", target: "/accounts/42/schedulable", body: `{"schedulable":true}`, bind: func(h *AccountHandler) gin.HandlerFunc { return h.SetSchedulable }},
		{name: "sync upstream models", method: http.MethodPost, route: "/accounts/:id/models/sync-upstream", target: "/accounts/42/models/sync-upstream", bind: func(h *AccountHandler) gin.HandlerFunc { return h.SyncUpstreamModels }},
		{name: "active usage refresh", method: http.MethodGet, route: "/accounts/:id/usage", target: "/accounts/42/usage?source=active&force=true", bind: func(h *AccountHandler) gin.HandlerFunc { return h.GetUsage }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			access := &accountManagementAccessSpy{denied: map[int64]error{42: remoteAccountReadOnlyError()}}
			handler := &AccountHandler{adminService: access}
			recorder := performAccountManagementRequest(t, test.method, test.route, test.target, test.body, test.bind(handler))

			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Equal(t, []int64{42}, access.calls)
		})
	}
}

func TestOpenAIQuotaMutationsRejectRemoteAccountBeforeQuotaSideEffects(t *testing.T) {
	for _, test := range []struct {
		name   string
		route  string
		target string
		bind   func(*OpenAIOAuthHandler) gin.HandlerFunc
	}{
		{name: "refresh quota snapshot", route: "/openai/accounts/:id/quota/refresh", target: "/openai/accounts/42/quota/refresh", bind: func(h *OpenAIOAuthHandler) gin.HandlerFunc { return h.RefreshQuota }},
		{name: "consume reset credit", route: "/openai/accounts/:id/reset-quota", target: "/openai/accounts/42/reset-quota", bind: func(h *OpenAIOAuthHandler) gin.HandlerFunc { return h.ResetQuota }},
	} {
		t.Run(test.name, func(t *testing.T) {
			access := &accountManagementAccessSpy{denied: map[int64]error{42: remoteAccountReadOnlyError()}}
			quota := &blockedOpenAIQuotaSpy{}
			handler := &OpenAIOAuthHandler{adminService: access, quotaService: quota}
			recorder := performAccountManagementRequest(t, http.MethodPost, test.route, test.target, "", test.bind(handler))

			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Equal(t, []int64{42}, access.calls)
			require.Zero(t, quota.queryCalls)
			require.Zero(t, quota.cacheCalls)
			require.Zero(t, quota.resetCalls)
		})
	}
}

func TestSpecializedOAuthMutationsRejectRemoteAccountBeforeDownstream(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		route      string
		target     string
		body       string
		middleware []gin.HandlerFunc
		bind       func(service.AdminService) gin.HandlerFunc
	}{
		{name: "OpenAI token refresh", method: http.MethodPost, route: "/openai/accounts/:id/refresh", target: "/openai/accounts/42/refresh", bind: func(adminService service.AdminService) gin.HandlerFunc {
			return (&OpenAIOAuthHandler{adminService: adminService}).RefreshAccountToken
		}},
		{name: "OpenAI shadow creation", method: http.MethodPost, route: "/accounts/:id/shadow", target: "/accounts/42/shadow", body: `{}`, bind: func(adminService service.AdminService) gin.HandlerFunc {
			return (&OpenAIOAuthHandler{adminService: adminService}).CreateShadow
		}},
		{name: "OpenAI reauthorization credential save", method: http.MethodPost, route: "/openai/accounts/:id/reauthorization-credentials", target: "/openai/accounts/42/reauthorization-credentials", body: `{"email":"user@example.test"}`, middleware: []gin.HandlerFunc{teamChildAdminTestMiddleware("admin")}, bind: func(adminService service.AdminService) gin.HandlerFunc {
			return (&OpenAIOAuthHandler{adminService: adminService, secretEncryptor: teamChildTestEncryptor{}}).SaveOpenAIAccountReauthorizationCredentials
		}},
		{name: "OpenAI reauthorization workflow", method: http.MethodPost, route: "/openai/accounts/:id/reauthorize", target: "/openai/accounts/42/reauthorize", body: `{}`, middleware: []gin.HandlerFunc{teamChildAdminTestMiddleware("admin")}, bind: func(adminService service.AdminService) gin.HandlerFunc {
			return (&OpenAIOAuthHandler{adminService: adminService}).ReauthorizeOpenAIAccount
		}},
		{name: "Grok token refresh", method: http.MethodPost, route: "/grok/accounts/:id/refresh", target: "/grok/accounts/42/refresh", bind: func(adminService service.AdminService) gin.HandlerFunc {
			return (&GrokOAuthHandler{adminService: adminService}).RefreshAccountToken
		}},
		{name: "Grok quota reset", method: http.MethodPost, route: "/grok/accounts/:id/reset-quota", target: "/grok/accounts/42/reset-quota", bind: func(adminService service.AdminService) gin.HandlerFunc {
			return (&GrokOAuthHandler{adminService: adminService}).ResetQuota
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			access := &accountManagementAccessSpy{denied: map[int64]error{42: remoteAccountReadOnlyError()}}
			recorder := performAccountManagementRequest(t, test.method, test.route, test.target, test.body, test.bind(access), test.middleware...)

			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Equal(t, []int64{42}, access.calls)
		})
	}
}

func TestUpstreamBillingProbeMutationsPreflightAccountOwnership(t *testing.T) {
	t.Run("single account endpoints", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			method string
			route  string
			target string
			body   string
			bind   func(*AccountHandler) gin.HandlerFunc
		}{
			{name: "toggle", method: http.MethodPut, route: "/accounts/:id/upstream-billing-probe", target: "/accounts/42/upstream-billing-probe", body: `{"enabled":true}`, bind: func(h *AccountHandler) gin.HandlerFunc { return h.SetUpstreamBillingProbeEnabled }},
			{name: "probe", method: http.MethodPost, route: "/accounts/:id/upstream-billing-probe", target: "/accounts/42/upstream-billing-probe", bind: func(h *AccountHandler) gin.HandlerFunc { return h.ProbeUpstreamBilling }},
		} {
			t.Run(test.name, func(t *testing.T) {
				access := &accountManagementAccessSpy{denied: map[int64]error{42: remoteAccountReadOnlyError()}}
				handler := &AccountHandler{adminService: access}
				recorder := performAccountManagementRequest(t, test.method, test.route, test.target, test.body, test.bind(handler))

				require.Equal(t, http.StatusForbidden, recorder.Code)
				require.Equal(t, []int64{42}, access.calls)
			})
		}
	})

	t.Run("batch checks every preceding account before any probe", func(t *testing.T) {
		access := &accountManagementAccessSpy{denied: map[int64]error{42: remoteAccountReadOnlyError()}}
		handler := &AccountHandler{adminService: access}
		recorder := performAccountManagementRequest(t, http.MethodPost, "/accounts/upstream-billing-probe/batch", "/accounts/upstream-billing-probe/batch", `{"account_ids":[41,42,43]}`, handler.ProbeUpstreamBillingBatch)

		require.Equal(t, http.StatusForbidden, recorder.Code)
		require.Equal(t, []int64{41, 42}, access.calls)
	})
}

func TestOllamaCloudUsageMutationsRejectRemoteAccountBeforeService(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		route  string
		target string
		body   string
		bind   func(*AccountHandler) gin.HandlerFunc
	}{
		{name: "save session", method: http.MethodPut, route: "/accounts/:id/ollama-cloud-usage/session", target: "/accounts/42/ollama-cloud-usage/session", body: `{"session":"secret"}`, bind: func(h *AccountHandler) gin.HandlerFunc { return h.SaveOllamaCloudUsageSession }},
		{name: "delete session", method: http.MethodDelete, route: "/accounts/:id/ollama-cloud-usage/session", target: "/accounts/42/ollama-cloud-usage/session", bind: func(h *AccountHandler) gin.HandlerFunc { return h.DeleteOllamaCloudUsageSession }},
		{name: "toggle refresh", method: http.MethodPut, route: "/accounts/:id/ollama-cloud-usage/auto-refresh", target: "/accounts/42/ollama-cloud-usage/auto-refresh", body: `{"enabled":true}`, bind: func(h *AccountHandler) gin.HandlerFunc { return h.SetOllamaCloudUsageAutoRefresh }},
		{name: "refresh", method: http.MethodPost, route: "/accounts/:id/ollama-cloud-usage/refresh", target: "/accounts/42/ollama-cloud-usage/refresh", bind: func(h *AccountHandler) gin.HandlerFunc { return h.RefreshOllamaCloudUsage }},
	} {
		t.Run(test.name, func(t *testing.T) {
			access := &accountManagementAccessSpy{denied: map[int64]error{42: remoteAccountReadOnlyError()}}
			handler := &AccountHandler{adminService: access}
			recorder := performAccountManagementRequest(t, test.method, test.route, test.target, test.body, test.bind(handler))

			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Equal(t, []int64{42}, access.calls)
		})
	}
}
