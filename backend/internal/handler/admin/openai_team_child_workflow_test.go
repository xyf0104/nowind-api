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
		`{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"https://auth.openai.com/authorize","confirmed":false}`,
		`{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"https://example.test/authorize","confirmed":true}`,
		`{"seat_email":"member@example.test","invite_email":"member@example.test","auth_url":"https://auth.openai.com/authorize","confirmed":true}`,
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	}
	require.False(t, called)
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

	payload := `{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"https://auth.openai.com/authorize?client_id=example","confirmed":true}`
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
	request := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader("{\"seat_email\":\"protected-admin@example.test\",\"invite_email\":\"new@example.test\",\"auth_url\":\"https://auth.openai.com/authorize\",\"confirmed\":true}"))
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

func TestValidateTeamChildWorkflowAuthURL(t *testing.T) {
	for _, raw := range []string{
		"https://auth.openai.com/authorize?client_id=example",
		"https://login.openai.com/authorize",
		"https://chatgpt.com/auth/login",
	} {
		require.NoError(t, validateTeamChildWorkflowAuthURL(raw))
	}
	for _, raw := range []string{
		"http://auth.openai.com/authorize",
		"https://user:pass@auth.openai.com/authorize",
		"https://auth.openai.com.example.test/authorize",
		"not a URL",
	} {
		require.Error(t, validateTeamChildWorkflowAuthURL(raw))
	}
}
