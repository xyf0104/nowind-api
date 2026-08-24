package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func teamChildAdminTestMiddleware(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		if role != "" {
			c.Set(string(middleware.ContextKeyUserRole), role)
		}
		c.Next()
	}
}

func TestTeamChildMemberAutomationRequiresAdministrator(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", "http://127.0.0.1:1")
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}

	for _, testCase := range []struct {
		name       string
		middleware gin.HandlerFunc
		status     int
	}{
		{name: "missing session", middleware: func(c *gin.Context) { c.Next() }, status: http.StatusUnauthorized},
		{name: "non admin", middleware: teamChildAdminTestMiddleware("user"), status: http.StatusForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := gin.New()
			router.Use(testCase.middleware)
			router.GET("/members", handler.ListTeamChildMembers)
			request := httptest.NewRequest(http.MethodGet, "/members", nil)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, testCase.status, recorder.Code)
		})
	}
}

func TestTeamChildMemberAutomationUsesEmailAndServiceToken(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var receivedToken string
	var receivedMethod string
	var receivedPath string
	var receivedBody map[string]any
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedToken = r.Header.Get("X-XIASS-Team-Child-Token")
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ready":true,"members":[],"operation":{"type":"update","email":"member@example.test","role":"admin","confirmed":true}}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.PATCH("/members", handler.UpdateTeamChildMember)

	request := httptest.NewRequest(http.MethodPatch, "/members", strings.NewReader("{"))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	// The validation above intentionally proves that an empty/invalid request is
	// rejected before any downstream call. Use a second request for the protocol.
	request = httptest.NewRequest(http.MethodPatch, "/members", strings.NewReader(`{"email":"member@example.test","role":"admin"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "service-token", receivedToken)
	require.Equal(t, http.MethodPatch, receivedMethod)
	require.Equal(t, "/members", receivedPath)
	require.Equal(t, "member@example.test", receivedBody["email"])
	require.Equal(t, "admin", receivedBody["role"])
}

func TestTeamChildMemberAutomationInspectRouteIsForwarded(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var receivedPath string
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ready":true,"members":[],"seat_email":""}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/members/inspect", handler.InspectTeamChildSeat)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/members/inspect", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "/members/inspect", receivedPath)
}

func TestTeamChildMemberInviteNormalizesTemporaryMailboxBeforeForwarding(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var receivedBody map[string]any
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ready":true,"members":[],"pending_invites":1,"operation":{"type":"invite","email":"nba0nfm7d7@ncml1.top","confirmed":true}}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/members/invite", handler.InviteTeamChildMember)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/members/invite", strings.NewReader(`{"email":"成员邮箱：NBA0NFM7D7@NCML1.TOP"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "nba0nfm7d7@ncml1.top", receivedBody["email"])
}

func TestTeamChildProtectedMemberCannotBeChangedOrRemoved(t *testing.T) {
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
	router.PATCH("/members", handler.UpdateTeamChildMember)
	router.DELETE("/members", handler.RemoveTeamChildMember)

	for _, testCase := range []struct {
		method string
		body   string
	}{
		{method: http.MethodPatch, body: "{\"email\":\"protected-admin@example.test\",\"role\":\"member\"}"},
		{method: http.MethodDelete, body: "{\"email\":\"protected-admin@example.test\"}"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(testCase.method, "/members", strings.NewReader(testCase.body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusForbidden, recorder.Code)
	}
	require.False(t, called)
}
