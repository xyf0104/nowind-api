package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountAdminBoundariesRejectMalformedOpenAILongContextBillingValue(t *testing.T) {
	const malformedExtra = `"extra":{"openai_long_context_billing_enabled":"true"}`

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		mount  func(*gin.Engine, *AccountHandler)
		setup  func(*stubAdminService)
	}{
		{
			name:   "create",
			method: http.MethodPost,
			path:   "/accounts",
			body:   `{"name":"account","platform":"openai","type":"apikey","credentials":{"api_key":"test"},` + malformedExtra + `}`,
			mount:  func(router *gin.Engine, handler *AccountHandler) { router.POST("/accounts", handler.Create) },
		},
		{
			name:   "update",
			method: http.MethodPut,
			path:   "/accounts/1",
			body:   `{` + malformedExtra + `}`,
			mount:  func(router *gin.Engine, handler *AccountHandler) { router.PUT("/accounts/:id", handler.Update) },
			setup: func(stub *stubAdminService) {
				stub.updateAccountErr = infraerrors.BadRequest("OPENAI_LONG_CONTEXT_BILLING_INVALID", "invalid")
			},
		},
		{
			name:   "bulk update",
			method: http.MethodPost,
			path:   "/accounts/bulk-update",
			body:   `{"account_ids":[1],` + malformedExtra + `}`,
			mount: func(router *gin.Engine, handler *AccountHandler) {
				router.POST("/accounts/bulk-update", handler.BulkUpdate)
			},
			setup: func(stub *stubAdminService) {
				stub.bulkUpdateAccountErr = infraerrors.BadRequest("OPENAI_LONG_CONTEXT_BILLING_INVALID", "invalid")
			},
		},
		{
			name:   "batch create",
			method: http.MethodPost,
			path:   "/accounts/batch",
			body:   `{"accounts":[{"name":"account","platform":"openai","type":"apikey","credentials":{"api_key":"test"},` + malformedExtra + `}]}`,
			mount:  func(router *gin.Engine, handler *AccountHandler) { router.POST("/accounts/batch", handler.BatchCreate) },
		},
		{
			name:   "Codex session import",
			method: http.MethodPost,
			path:   "/accounts/import-codex-session",
			body:   `{"content":"token",` + malformedExtra + `}`,
			mount: func(router *gin.Engine, handler *AccountHandler) {
				router.POST("/accounts/import-codex-session", handler.ImportCodexSession)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			stub := newStubAdminService()
			if tt.setup != nil {
				tt.setup(stub)
			}
			handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			tt.mount(router, handler)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			request.Header.Set("Content-Type", "application/json")

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			var responseBody struct {
				Reason string `json:"reason"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &responseBody))
			require.Equal(t, "OPENAI_LONG_CONTEXT_BILLING_INVALID", responseBody.Reason)
		})
	}
}

func TestAccountCreateBoundaryDoesNotApplyOpenAIValidationToOtherPlatforms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAccountHandler(newStubAdminService(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/accounts", handler.Create)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/accounts", bytes.NewBufferString(
		`{"name":"account","platform":"anthropic","type":"apikey","credentials":{"api_key":"test"},"extra":{"openai_long_context_billing_enabled":"provider-owned"}}`,
	))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestApplyOAuthCredentialsRejectsMalformedOpenAILongContextBillingBeforeMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := newStubAdminService()
	stub.getAccountResult = &service.Account{
		ID:       1,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
	}
	handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/accounts/:id/apply-oauth-credentials", handler.ApplyOAuthCredentials)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/accounts/1/apply-oauth-credentials", bytes.NewBufferString(
		`{"type":"oauth","credentials":{"access_token":"new-token"},"extra":{"openai_long_context_billing_enabled":"true"}}`,
	))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var responseBody struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &responseBody))
	require.Equal(t, "OPENAI_LONG_CONTEXT_BILLING_INVALID", responseBody.Reason)
	require.Zero(t, stub.updateAccountCalls)
	require.Zero(t, stub.updateAccountExtraCalls)
}

func TestApplyOAuthCredentialsKeepsTeamChildMailboxIdentity(t *testing.T) {
	newHandler := func(stub *stubAdminService) (*gin.Engine, *AccountHandler) {
		stub.getAccountResult = &service.Account{
			ID:       317,
			Name:     "old-mailbox@example.test",
			Platform: service.PlatformOpenAI,
			Type:     service.AccountTypeOAuth,
			Extra: map[string]any{
				service.OpenAITeamChildExtraKey:      true,
				service.OpenAITeamChildEmailExtraKey: "team1003@example.test",
			},
		}
		handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		router := gin.New()
		router.POST("/accounts/:id/apply-oauth-credentials", handler.ApplyOAuthCredentials)
		return router, handler
	}

	t.Run("normalizes matching email and repairs the account name", func(t *testing.T) {
		stub := newStubAdminService()
		router, _ := newHandler(stub)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/accounts/317/apply-oauth-credentials", bytes.NewBufferString(
			`{"type":"oauth","credentials":{"access_token":"new-token","email":"Team1003@Example.Test"}}`,
		))
		request.Header.Set("Content-Type", "application/json")

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Equal(t, "team1003@example.test", stub.lastUpdateAccountInput.Name)
		require.Equal(t, "team1003@example.test", stub.lastUpdateAccountInput.Credentials["email"])
	})

	t.Run("rejects a different OAuth subject before mutation", func(t *testing.T) {
		stub := newStubAdminService()
		router, _ := newHandler(stub)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/accounts/317/apply-oauth-credentials", bytes.NewBufferString(
			`{"type":"oauth","credentials":{"access_token":"new-token","email":"other@example.test"}}`,
		))
		request.Header.Set("Content-Type", "application/json")

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Zero(t, stub.updateAccountCalls)
	})

	t.Run("preserves the verified email when the new token has no email claim", func(t *testing.T) {
		stub := newStubAdminService()
		router, _ := newHandler(stub)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/accounts/317/apply-oauth-credentials", bytes.NewBufferString(
			`{"type":"oauth","credentials":{"access_token":"new-token"}}`,
		))
		request.Header.Set("Content-Type", "application/json")

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Equal(t, "team1003@example.test", stub.lastUpdateAccountInput.Credentials["email"])
	})
}

func TestOpenAIOAuthCodexPATBoundaryRejectsMalformedOpenAILongContextBillingValueBeforeTokenValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewOpenAIOAuthHandler(nil, newStubAdminService(), nil, nil)
	router := gin.New()
	router.Use(gin.Recovery())
	router.POST("/openai/create-from-codex-pat", handler.CreateAccountFromCodexPAT)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/openai/create-from-codex-pat", bytes.NewBufferString(
		`{"access_token":"token","extra":{"openai_long_context_billing_enabled":1}}`,
	))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var responseBody struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &responseBody))
	require.Equal(t, "OPENAI_LONG_CONTEXT_BILLING_INVALID", responseBody.Reason)
}
