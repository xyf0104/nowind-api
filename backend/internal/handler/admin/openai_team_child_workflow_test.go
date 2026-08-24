package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const testTeamChildAuthURL = "https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=test-challenge&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=test-state"

func TestTeamChildWorkflowStartRequiresConfirmedSupportedOpenAIURL(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	called := false
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows", handler.StartTeamChildWorkflow)

	for _, payload := range []string{
		`{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"` + testTeamChildAuthURL + `","confirmed":false}`,
		`{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"https://example.test/authorize","confirmed":true}`,
		`{"seat_email":"member@example.test","invite_email":"member@example.test","auth_url":"` + testTeamChildAuthURL + `","confirmed":true}`,
		`{"invite_email":"new@example.test","auth_url":"` + testTeamChildAuthURL + `","confirmed":true}`,
		`{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"` + testTeamChildAuthURL + `","seat_already_removed":true,"confirmed":true}`,
		`{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"` + testTeamChildAuthURL + `","start_step":"invalid","confirmed":true}`,
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	}
	require.False(t, called)
}

func TestTeamChildWorkflowRunStepValidatesAndForwards(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var receivedMethod string
	var receivedPath string
	var receivedBody map[string]any
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abcdefghijklmnoP","status":"manual_required","steps":[]}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows/:workflow_id/run-step", handler.RunTeamChildWorkflowStep)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/workflows/abcdefghijklmnoP/run-step", strings.NewReader(`{"step":"oauth"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, http.MethodPost, receivedMethod)
	require.Equal(t, "/workflows/abcdefghijklmnoP/run-step", receivedPath)
	require.Equal(t, "oauth", receivedBody["step"])

	for _, payload := range []string{`{}`, `{"step":""}`, `{"step":"invalid"}`} {
		recorder = httptest.NewRecorder()
		request = httptest.NewRequest(http.MethodPost, "/workflows/abcdefghijklmnoP/run-step", strings.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/workflows/too-short/run-step", strings.NewReader(`{"step":"oauth"}`)))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestTeamChildWorkflowProxyPreservesOnlyConfirmedWorkflowRequest(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var receivedToken string
	var receivedMethod string
	var receivedPath string
	var receivedBody map[string]any
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedToken = r.Header.Get("X-XIASS-Team-Child-Token")
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abcdefghijklmnoP","status":"running","steps":[]}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows", handler.StartTeamChildWorkflow)

	payload := `{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"` + testTeamChildAuthURL + `","start_step":"oauth","run_only_step":true,"confirmed":true}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "service-token", receivedToken)
	require.Equal(t, http.MethodPost, receivedMethod)
	require.Equal(t, "/workflows", receivedPath)
	require.Equal(t, "member@example.test", receivedBody["seat_email"])
	require.Equal(t, "new@example.test", receivedBody["invite_email"])
	require.Equal(t, false, receivedBody["seat_already_removed"])
	require.Equal(t, "oauth", receivedBody["start_step"])
	require.Equal(t, true, receivedBody["run_only_step"])
	require.Equal(t, true, receivedBody["confirmed"])
}

func TestTeamChildWorkflowAllowsConfirmedManualSeatRelease(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var receivedBody map[string]any
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abcdefghijklmnoP","status":"running","steps":[]}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows", handler.StartTeamChildWorkflow)

	payload := `{"invite_email":"new@example.test","auth_url":"` + testTeamChildAuthURL + `","seat_already_removed":true,"confirmed":true}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, receivedBody["seat_email"])
	require.Equal(t, "new@example.test", receivedBody["invite_email"])
	require.Equal(t, true, receivedBody["seat_already_removed"])
	require.Equal(t, true, receivedBody["confirmed"])
}

func TestTeamChildWorkflowRejectsProtectedReplacementSeat(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	t.Setenv("TEAM_CHILD_PROTECTED_MEMBER_EMAILS", "protected-admin@example.test")
	called := false
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows", handler.StartTeamChildWorkflow)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(`{"seat_email":"protected-admin@example.test","invite_email":"new@example.test","auth_url":"`+testTeamChildAuthURL+`","confirmed":true}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.False(t, called)
}

func TestTeamChildWorkflowContinueValidatesIDAndForwards(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var receivedMethod string
	var receivedPath string
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"id\":\"abcdefghijklmnoP\",\"status\":\"running\",\"steps\":[]}"))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows/:workflow_id/continue", handler.ContinueTeamChildWorkflow)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/workflows/abcdefghijklmnoP/continue", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, http.MethodPost, receivedMethod)
	require.Equal(t, "/workflows/abcdefghijklmnoP/continue", receivedPath)

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/workflows/too-short/continue", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestTeamChildWorkflowStatusAndCancelValidateIDAndForward(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var requests []string
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abcdefghijklmnoP","status":"manual_required","steps":[]}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.GET("/workflows/:workflow_id", handler.GetTeamChildWorkflow)
	router.DELETE("/workflows/:workflow_id", handler.CancelTeamChildWorkflow)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(method, "/workflows/abcdefghijklmnoP", nil))
		require.Equal(t, http.StatusOK, recorder.Code)
	}
	require.Equal(t, []string{
		"GET /workflows/abcdefghijklmnoP",
		"DELETE /workflows/abcdefghijklmnoP",
	}, requests)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/workflows/too-short", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestTeamChildWorkflowCallbackAndRestartForward(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var requests []struct {
		method string
		path   string
		body   map[string]any
	}
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := struct {
			method string
			path   string
			body   map[string]any
		}{method: r.Method, path: r.URL.Path}
		if r.Body != nil {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request.body))
		}
		requests = append(requests, request)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abcdefghijklmnoP","status":"manual_required","steps":[]}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows/:workflow_id/callback", handler.SubmitTeamChildWorkflowCallback)
	router.POST("/workflows/:workflow_id/restart-oauth", handler.RestartTeamChildWorkflowOAuth)

	for _, test := range []struct {
		path    string
		payload string
	}{
		{path: "/workflows/abcdefghijklmnoP/callback", payload: `{"callback_url":"http://localhost:1455/auth/callback?code=callback-code&state=callback-state"}`},
		{path: "/workflows/abcdefghijklmnoP/restart-oauth", payload: `{"auth_url":"` + testTeamChildAuthURL + `"}`},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.payload))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code)
	}

	require.Len(t, requests, 2)
	require.Equal(t, "/workflows/abcdefghijklmnoP/callback", requests[0].path)
	require.Equal(t, "http://localhost:1455/auth/callback?code=callback-code&state=callback-state", requests[0].body["callback_url"])
	require.Equal(t, "/workflows/abcdefghijklmnoP/restart-oauth", requests[1].path)
	require.Equal(t, testTeamChildAuthURL, requests[1].body["auth_url"])

	for _, test := range []struct {
		path    string
		payload string
	}{
		{path: "/workflows/abcdefghijklmnoP/callback", payload: `{"callback_url":"http://localhost:1455/auth/callback?code=only-code"}`},
		{path: "/workflows/abcdefghijklmnoP/restart-oauth", payload: `{"auth_url":"https://example.test/authorize"}`},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.payload))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	}
	require.Len(t, requests, 2)
}

func TestTeamChildWorkflowLegacyExternalValueRoutesRejectPayload(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	called := false
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows/:workflow_id/phone", handler.RejectTeamChildWorkflowExternalValue)
	router.POST("/workflows/:workflow_id/code", handler.RejectTeamChildWorkflowExternalValue)

	for _, path := range []string{"/workflows/abcdefghijklmnoP/phone", "/workflows/abcdefghijklmnoP/code"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{\"phone\":\"+15551234567\",\"code\":\"123456\"}"))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Contains(t, recorder.Body.String(), "不接收或转发")
	}
	require.False(t, called)
}

func TestValidateTeamChildWorkflowAuthURL(t *testing.T) {
	for _, raw := range []string{
		testTeamChildAuthURL,
	} {
		require.NoError(t, validateTeamChildWorkflowAuthURL(raw))
	}
	for _, raw := range []string{
		"http://auth.openai.com/oauth/authorize?client_id=x&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&scope=openid&state=s&code_challenge=c&code_challenge_method=S256",
		"https://user:pass@auth.openai.com/oauth/authorize?client_id=x&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&scope=openid&state=s&code_challenge=c&code_challenge_method=S256",
		"https://auth.openai.com.example.test/oauth/authorize?client_id=x&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&scope=openid&state=s&code_challenge=c&code_challenge_method=S256",
		"https://auth.openai.com/authorize?client_id=example",
		"https://auth.openai.com/oauth/authorize?client_id=wrong&code_challenge=test-challenge&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=test-state",
		"https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=test-challenge&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=https%3A%2F%2Fexample.test%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=test-state",
		"https://login.openai.com/authorize",
		"not a URL",
	} {
		require.Error(t, validateTeamChildWorkflowAuthURL(raw))
	}
}

func TestNormalizeTeamChildWorkflowEmailAcceptsTemporaryMailboxAndDisplayText(t *testing.T) {
	for _, test := range []struct {
		input    string
		expected string
	}{
		{input: "nba0nfm7d7@ncml1.top", expected: "nba0nfm7d7@ncml1.top"},
		{input: "  成员邮箱：NBA0NFM7D7@NCML1.TOP  ", expected: "nba0nfm7d7@ncml1.top"},
	} {
		actual := normalizeTeamChildWorkflowEmail(test.input)
		require.Equal(t, test.expected, actual)
		require.True(t, validTeamChildWorkflowEmail(actual))
	}

	for _, input := range []string{"", "not-an-email", "name@localhost"} {
		require.False(t, validTeamChildWorkflowEmail(normalizeTeamChildWorkflowEmail(input)), input)
	}
}
