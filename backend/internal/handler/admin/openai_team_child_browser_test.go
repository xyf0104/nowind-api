package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func setupTeamChildBrowserTestRouter(t *testing.T, upstream http.HandlerFunc) (*OpenAIOAuthHandler, *gin.Engine) {
	t.Helper()
	upstreamServer := httptest.NewServer(upstream)
	t.Cleanup(upstreamServer.Close)
	t.Setenv("TEAM_CHILD_BROWSER_ENABLED", "true")
	t.Setenv("TEAM_CHILD_BROWSER_UPSTREAM_URL", upstreamServer.URL)
	t.Setenv("TEAM_CHILD_BROWSER_SESSION_TTL_MINUTES", "60")

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{teamBrowserStore: newOpenAITeamBrowserStore()}
	router := gin.New()
	router.POST("/sessions", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	}, handler.CreateTeamChildBrowserSession)
	router.GET(teamChildBrowserProxyPrefix+"/*path", handler.ServeTeamChildBrowser)
	return handler, router
}

func TestTeamChildBrowserSessionRequiresConfiguredBrowser(t *testing.T) {
	t.Setenv("TEAM_CHILD_BROWSER_ENABLED", "false")
	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{teamBrowserStore: newOpenAITeamBrowserStore()}
	router := gin.New()
	router.POST("/sessions", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	}, handler.CreateTeamChildBrowserSession)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/sessions", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "UPSTREAM_URL")
}

func TestTeamChildBrowserTicketIsOneTimeAndExchangedForHttpOnlyCookie(t *testing.T) {
	_, router := setupTeamChildBrowserTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	createRecorder := httptest.NewRecorder()
	router.ServeHTTP(createRecorder, httptest.NewRequest(http.MethodPost, "/sessions", nil))
	require.Equal(t, http.StatusOK, createRecorder.Code)
	var createResponse struct {
		Data teamChildBrowserSessionResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRecorder.Body.Bytes(), &createResponse))
	require.NotEmpty(t, createResponse.Data.EmbedURL)
	require.NotContains(t, createResponse.Data.EmbedURL, "127.0.0.1")

	bootstrapURL, err := url.Parse(createResponse.Data.EmbedURL)
	require.NoError(t, err)
	require.NotEmpty(t, bootstrapURL.Query().Get("ticket"))

	bootstrapRecorder := httptest.NewRecorder()
	router.ServeHTTP(bootstrapRecorder, httptest.NewRequest(http.MethodGet, bootstrapURL.String(), nil))
	require.Equal(t, http.StatusFound, bootstrapRecorder.Code)
	require.Equal(t, teamChildBrowserProxyPrefix+"/", bootstrapRecorder.Header().Get("Location"))
	cookies := bootstrapRecorder.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, teamChildBrowserCookieName, cookies[0].Name)
	require.True(t, cookies[0].HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, cookies[0].SameSite)
	require.Equal(t, teamChildBrowserProxyPrefix, cookies[0].Path)

	replayRecorder := httptest.NewRecorder()
	router.ServeHTTP(replayRecorder, httptest.NewRequest(http.MethodGet, bootstrapURL.String(), nil))
	require.Equal(t, http.StatusUnauthorized, replayRecorder.Code)
}

func TestTeamChildBrowserProxyRemovesPanelCredentialsAndFrameRestrictions(t *testing.T) {
	var receivedPath string
	var receivedAuthorization string
	var receivedAPIKey string
	var receivedCookie string
	_, router := setupTeamChildBrowserTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedAuthorization = r.Header.Get("Authorization")
		receivedAPIKey = r.Header.Get("X-API-Key")
		receivedCookie = r.Header.Get("Cookie")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; img-src data:")
		_, _ = w.Write([]byte("remote Chromium"))
	})

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	createRequest, err := http.NewRequest(http.MethodPost, server.URL+"/sessions", nil)
	require.NoError(t, err)
	createResponseHTTP, err := client.Do(createRequest)
	require.NoError(t, err)
	t.Cleanup(func() { _ = createResponseHTTP.Body.Close() })
	require.Equal(t, http.StatusOK, createResponseHTTP.StatusCode)
	var createResponse struct {
		Data teamChildBrowserSessionResponse `json:"data"`
	}
	require.NoError(t, json.NewDecoder(createResponseHTTP.Body).Decode(&createResponse))

	bootstrapResponse, err := client.Get(server.URL + createResponse.Data.EmbedURL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = bootstrapResponse.Body.Close() })
	require.Equal(t, http.StatusFound, bootstrapResponse.StatusCode)

	proxyRequest, err := http.NewRequest(http.MethodGet, server.URL+teamChildBrowserProxyPrefix+"/desktop/", nil)
	require.NoError(t, err)
	proxyRequest.Header.Set("Authorization", "Bearer panel-secret")
	proxyRequest.Header.Set("X-API-Key", "panel-api-key")
	proxyResponse, err := client.Do(proxyRequest)
	require.NoError(t, err)
	t.Cleanup(func() { _ = proxyResponse.Body.Close() })

	require.Equal(t, http.StatusOK, proxyResponse.StatusCode)
	require.Equal(t, teamChildBrowserProxyPrefix+"/desktop/", receivedPath)
	require.Empty(t, receivedAuthorization)
	require.Empty(t, receivedAPIKey)
	require.Empty(t, receivedCookie)
	require.Empty(t, proxyResponse.Header.Get("X-Frame-Options"))
	require.Equal(t, "default-src 'self'; img-src data:", proxyResponse.Header.Get("Content-Security-Policy"))
	require.Contains(t, proxyResponse.Header.Get("Cache-Control"), "no-store")
	proxyBody, err := io.ReadAll(proxyResponse.Body)
	require.NoError(t, err)
	require.Equal(t, "remote Chromium", string(proxyBody))
}

func TestTeamChildBrowserExpiredSessionIsRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now()
	handler := &OpenAIOAuthHandler{teamBrowserStore: newOpenAITeamBrowserStore()}
	handler.teamBrowserStore.now = func() time.Time { return now }
	handler.teamBrowserStore.sessions["expired"] = openAITeamBrowserSession{
		expiresAt: now.Add(-time.Second),
	}
	router := gin.New()
	router.GET(teamChildBrowserProxyPrefix+"/*path", handler.ServeTeamChildBrowser)

	request := httptest.NewRequest(http.MethodGet, teamChildBrowserProxyPrefix+"/", nil)
	request.AddCookie(&http.Cookie{Name: teamChildBrowserCookieName, Value: "expired"})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestTeamChildBrowserRedisStorePersistsAndConsumesTicketAcrossHandlers(t *testing.T) {
	miniRedis := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: miniRedis.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	first := newOpenAITeamBrowserStore()
	first.configureRedis(client)
	second := newOpenAITeamBrowserStore()
	second.configureRedis(client)
	upstream, err := url.Parse("https://team-child-browser:3001")
	require.NoError(t, err)
	session := openAITeamBrowserSession{
		adminUserID: 42,
		upstreamURL: upstream,
		expiresAt:   time.Now().Add(time.Minute),
	}
	require.NoError(t, first.create(t.Context(), "session-token", "one-time-ticket", session, time.Minute))

	loaded, ok, err := second.lookup(t.Context(), "session-token")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(42), loaded.adminUserID)

	_, consumed, ok, err := second.consumeTicket(t.Context(), "one-time-ticket")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(42), consumed.adminUserID)
	_, _, ok, err = first.consumeTicket(t.Context(), "one-time-ticket")
	require.NoError(t, err)
	require.False(t, ok)
}
