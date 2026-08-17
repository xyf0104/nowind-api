package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	basehandler "github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type publicToolOAuthClientStub struct {
	redirectURI string
	proxyURL    string
}

func (s *publicToolOAuthClientStub) ExchangeCode(_ context.Context, _, _, redirectURI, proxyURL, _ string) (*openai.TokenResponse, error) {
	s.redirectURI = redirectURI
	s.proxyURL = proxyURL
	return &openai.TokenResponse{
		AccessToken:  "public-access-token",
		RefreshToken: "public-refresh-token",
		ExpiresIn:    3600,
	}, nil
}

func (s *publicToolOAuthClientStub) RefreshToken(context.Context, string, string) (*openai.TokenResponse, error) {
	panic("not used")
}

func (s *publicToolOAuthClientStub) RefreshTokenWithClientID(context.Context, string, string, string) (*openai.TokenResponse, error) {
	panic("not used")
}

func newPublicToolRouter(t *testing.T) (*gin.Engine, *publicToolOAuthClientStub) {
	return newPublicToolRouterWithTrustedProxies(t, nil)
}

func newPublicToolRouterWithTrustedProxies(t *testing.T, trustedProxies []string) (*gin.Engine, *publicToolOAuthClientStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })

	oauthClient := &publicToolOAuthClientStub{}
	oauthService := service.NewOpenAIOAuthService(nil, oauthClient)
	t.Cleanup(oauthService.Stop)
	openAIHandler := adminhandler.NewOpenAIOAuthHandler(oauthService, nil, nil, nil)
	handlers := &basehandler.Handlers{
		Admin: &basehandler.AdminHandlers{OpenAIOAuth: openAIHandler},
	}

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(trustedProxies))
	// Mirror an upgraded deployment still using the legacy forwarded-header
	// compatibility mode. The public OAuth route must override this snapshot.
	cfg := &config.Config{}
	cfg.SetTrustForwardedIPForAPIKeyACL(true)
	router.Use(servermiddleware.SessionBindingContext(cfg))
	RegisterToolRoutes(router.Group("/api/v1"), handlers, redisClient, nil)
	return router, oauthClient
}

func performToolRequest(router http.Handler, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return performToolRequestFrom(router, method, path, body, "203.0.113.20:45678", nil, cookies...)
}

func performToolRequestFrom(
	router http.Handler,
	method string,
	path string,
	body any,
	remoteAddr string,
	headers map[string]string,
	cookies ...*http.Cookie,
) *httptest.ResponseRecorder {
	var requestBody bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&requestBody).Encode(body)
	}
	req := httptest.NewRequest(method, path, &requestBody)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func performRawToolRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.20:45678"
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func requirePublicToolSecurityHeaders(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, "private, no-store, max-age=0", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "no-cache", recorder.Header().Get("Pragma"))
	require.Equal(t, "0", recorder.Header().Get("Expires"))
	require.Equal(t, "no-referrer", recorder.Header().Get("Referrer-Policy"))
}

func TestPublicOpenAIOAuthToolCompletesWithoutAuthenticationOrInjectedRouting(t *testing.T) {
	router, oauthClient := newPublicToolRouter(t)

	startRecorder := performToolRequest(router, http.MethodPost, "/api/v1/tools/openai-oauth/authorize", map[string]any{
		"proxy_id":     123,
		"redirect_uri": "https://attacker.example/callback",
	})
	require.Equal(t, http.StatusOK, startRecorder.Code)
	requirePublicToolSecurityHeaders(t, startRecorder)
	cookies := startRecorder.Result().Cookies()
	require.Len(t, cookies, 1)
	require.True(t, cookies[0].HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, cookies[0].SameSite)
	var startEnvelope struct {
		Data service.OpenAIAuthURLResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(startRecorder.Body.Bytes(), &startEnvelope))
	require.NotEmpty(t, startEnvelope.Data.SessionID)
	parsedAuthURL, err := url.Parse(startEnvelope.Data.AuthURL)
	require.NoError(t, err)
	require.Equal(t, openai.DefaultRedirectURI, parsedAuthURL.Query().Get("redirect_uri"))
	state := parsedAuthURL.Query().Get("state")
	require.NotEmpty(t, state)

	exchangeRecorder := performToolRequest(router, http.MethodPost, "/api/v1/tools/openai-oauth/exchange", map[string]any{
		"session_id":   startEnvelope.Data.SessionID,
		"code":         "authorization-code",
		"state":        state,
		"proxy_id":     456,
		"redirect_uri": "https://attacker.example/callback",
	}, cookies[0])
	require.Equal(t, http.StatusOK, exchangeRecorder.Code)
	requirePublicToolSecurityHeaders(t, exchangeRecorder)
	var tokenEnvelope struct {
		Data service.OpenAITokenInfo `json:"data"`
	}
	require.NoError(t, json.Unmarshal(exchangeRecorder.Body.Bytes(), &tokenEnvelope))
	require.Equal(t, "public-access-token", tokenEnvelope.Data.AccessToken)
	require.Equal(t, "public-refresh-token", tokenEnvelope.Data.RefreshToken)
	require.Equal(t, openai.DefaultRedirectURI, oauthClient.redirectURI)
	require.Empty(t, oauthClient.proxyURL)
}

func TestPublicOpenAIOAuthExchangeRequiresTheInitiatingBrowser(t *testing.T) {
	router, _ := newPublicToolRouter(t)
	startRecorder := performToolRequest(router, http.MethodPost, "/api/v1/tools/openai-oauth/authorize", map[string]any{})
	var startEnvelope struct {
		Data service.OpenAIAuthURLResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(startRecorder.Body.Bytes(), &startEnvelope))
	parsedAuthURL, err := url.Parse(startEnvelope.Data.AuthURL)
	require.NoError(t, err)

	exchangeRecorder := performToolRequest(router, http.MethodPost, "/api/v1/tools/openai-oauth/exchange", map[string]any{
		"session_id": startEnvelope.Data.SessionID,
		"code":       "authorization-code",
		"state":      parsedAuthURL.Query().Get("state"),
	})
	require.Equal(t, http.StatusBadRequest, exchangeRecorder.Code)
}

func TestPublicOpenAIOAuthSecurityHeadersCoverEarlyErrorsAndRateLimits(t *testing.T) {
	t.Run("malformed JSON", func(t *testing.T) {
		router, _ := newPublicToolRouter(t)
		recorder := performRawToolRequest(router, http.MethodPost, "/api/v1/tools/openai-oauth/exchange", `{"session_id":`)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		requirePublicToolSecurityHeaders(t, recorder)
	})

	t.Run("missing browser cookie", func(t *testing.T) {
		router, _ := newPublicToolRouter(t)
		recorder := performToolRequest(router, http.MethodPost, "/api/v1/tools/openai-oauth/exchange", map[string]any{
			"session_id": strings.Repeat("s", 32),
			"code":       "authorization-code",
			"state":      strings.Repeat("a", 64),
		})
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		requirePublicToolSecurityHeaders(t, recorder)
	})

	t.Run("rate limited", func(t *testing.T) {
		router, _ := newPublicToolRouter(t)
		var recorder *httptest.ResponseRecorder
		for requestNumber := 1; requestNumber <= 6; requestNumber++ {
			recorder = performToolRequest(router, http.MethodPost, "/api/v1/tools/openai-oauth/authorize", map[string]any{})
		}
		require.Equal(t, http.StatusTooManyRequests, recorder.Code)
		requirePublicToolSecurityHeaders(t, recorder)
	})
}

func TestPublicOpenAIOAuthAuthorizeIsStrictlyRateLimited(t *testing.T) {
	router, _ := newPublicToolRouter(t)
	for requestNumber := 1; requestNumber <= 6; requestNumber++ {
		recorder := performToolRequest(router, http.MethodPost, "/api/v1/tools/openai-oauth/authorize", map[string]any{})
		if requestNumber <= 5 {
			require.Equal(t, http.StatusOK, recorder.Code)
		} else {
			require.Equal(t, http.StatusTooManyRequests, recorder.Code)
		}
	}
}

func TestPublicOpenAIOAuthRateLimitIgnoresSpoofedForwardingHeadersFromDirectClients(t *testing.T) {
	router, _ := newPublicToolRouter(t)
	claimedIPs := []string{
		"198.51.100.1",
		"198.51.100.2",
		"198.51.100.3",
		"198.51.100.4",
		"198.51.100.5",
		"198.51.100.6",
	}
	for requestNumber, claimedIP := range claimedIPs {
		recorder := performToolRequestFrom(
			router,
			http.MethodPost,
			"/api/v1/tools/openai-oauth/authorize",
			map[string]any{},
			"203.0.113.20:45678",
			map[string]string{
				"CF-Connecting-IP": claimedIP,
				"X-Forwarded-For":  claimedIP,
			},
		)
		if requestNumber < 5 {
			require.Equal(t, http.StatusOK, recorder.Code)
		} else {
			require.Equal(t, http.StatusTooManyRequests, recorder.Code)
		}
	}
}

func TestPublicOpenAIOAuthRateLimitSeparatesClientsBehindTrustedProxy(t *testing.T) {
	router, _ := newPublicToolRouterWithTrustedProxies(t, []string{"127.0.0.1/32"})
	send := func(clientIP string) *httptest.ResponseRecorder {
		return performToolRequestFrom(
			router,
			http.MethodPost,
			"/api/v1/tools/openai-oauth/authorize",
			map[string]any{},
			"127.0.0.1:45678",
			map[string]string{"X-Forwarded-For": clientIP},
		)
	}

	for requestNumber := 1; requestNumber <= 5; requestNumber++ {
		require.Equal(t, http.StatusOK, send("198.51.100.10").Code)
	}
	require.Equal(t, http.StatusOK, send("198.51.100.11").Code)
	require.Equal(t, http.StatusTooManyRequests, send("198.51.100.10").Code)
}
