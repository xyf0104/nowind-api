package admin

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func setupTeamMailboxTestHandler(t *testing.T, provider http.HandlerFunc) (*OpenAIOAuthHandler, *gin.Engine) {
	t.Helper()
	server := httptest.NewServer(provider)
	t.Cleanup(server.Close)
	t.Setenv("TEAM_CHILD_MAIL_API_BASE", server.URL)
	t.Setenv("TEAM_CHILD_MAIL_AUTH_MODE", "x-api-key")
	t.Setenv("TEAM_CHILD_MAIL_API_KEY", "provider-api-key")
	t.Setenv("TEAM_CHILD_MAIL_CREATE_PATH", "/api/new_address")
	t.Setenv("TEAM_CHILD_MAIL_MESSAGES_PATH", "/api/mails")

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{teamMailboxStore: newOpenAITeamMailboxStore()}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	router.GET("/status", handler.TeamChildMailboxStatus)
	router.POST("/mailboxes", handler.CreateTeamChildMailbox)
	router.GET("/mailboxes/:session_id/code", handler.PollTeamChildMailboxCode)
	router.DELETE("/mailboxes/:session_id", handler.DeleteTeamChildMailboxSession)
	return handler, router
}

func TestTeamChildMailboxStatusIsDisabledWithoutProvider(t *testing.T) {
	t.Setenv("TEAM_CHILD_MAIL_API_BASE", "")
	t.Setenv("TEAM_CHILD_BROWSER_ENABLED", "false")
	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{teamMailboxStore: newOpenAITeamMailboxStore()}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	router.GET("/status", handler.TeamChildMailboxStatus)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var response struct {
		Data struct {
			Configured        bool `json:"configured"`
			BrowserConfigured bool `json:"browser_configured"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.False(t, response.Data.Configured)
	require.False(t, response.Data.BrowserConfigured)
}

func TestCreateTeamChildMailboxKeepsProviderTokenServerSide(t *testing.T) {
	_, router := setupTeamMailboxTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/new_address", r.URL.Path)
		require.Equal(t, "provider-api-key", r.Header.Get("X-API-Key"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		_, _ = w.Write([]byte(`{"address":"one@example.test","jwt":"mailbox-jwt-secret"}`))
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mailboxes", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "mailbox-jwt-secret")
	var response struct {
		Data openAITeamMailboxCreateResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.NotEmpty(t, response.Data.SessionID)
	require.Equal(t, "one@example.test", response.Data.Email)
}

func TestCreateTeamChildMailboxUsesAdminWorkerPayload(t *testing.T) {
	t.Setenv("TEAM_CHILD_MAIL_DOMAIN", "example.test")
	_, router := setupTeamMailboxTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Name         string `json:"name"`
			EnablePrefix bool   `json:"enablePrefix"`
			Domain       string `json:"domain"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Len(t, payload.Name, 10)
		require.True(t, payload.EnablePrefix)
		require.Equal(t, "example.test", payload.Domain)
		_, _ = w.Write([]byte(`{"address":"one@example.test","jwt":"mailbox-jwt-secret"}`))
	})
	t.Setenv("TEAM_CHILD_MAIL_CREATE_PATH", "/admin/new_address")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mailboxes", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTeamChildMailboxPollUsesMailboxJWTAndExtractsReadableVerificationCode(t *testing.T) {
	handler, router := setupTeamMailboxTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/new_address":
			_, _ = w.Write([]byte(`{"address":"one@example.test","jwt":"mailbox-jwt-secret"}`))
		case "/api/mails":
			require.Equal(t, "Bearer mailbox-jwt-secret", r.Header.Get("Authorization"))
			require.Equal(t, "", r.Header.Get("X-API-Key"))
			require.Equal(t, "20", r.URL.Query().Get("limit"))
			_, _ = w.Write([]byte(`{"messages":[{"id":"m-1","to":[{"address":"one@example.test"}],"subject":"OpenAI verification code: 418204"}]}`))
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	})

	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/mailboxes", nil))
	var createResponse struct {
		Data openAITeamMailboxCreateResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &createResponse))

	pollRec := httptest.NewRecorder()
	router.ServeHTTP(pollRec, httptest.NewRequest(http.MethodGet, "/mailboxes/"+createResponse.Data.SessionID+"/code", nil))
	require.Equal(t, http.StatusOK, pollRec.Code)
	var pollResponse struct {
		Data openAITeamMailboxCodeResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(pollRec.Body.Bytes(), &pollResponse))
	require.Equal(t, "received", pollResponse.Data.Status)
	require.Equal(t, "418204", pollResponse.Data.Code)

	// Ensure the actual session, not the HTTP response, owns the sensitive JWT.
	session, ok := handler.lookupTeamMailboxSession(createResponse.Data.SessionID)
	require.True(t, ok)
	require.Equal(t, "mailbox-jwt-secret", session.token)
}

func TestTeamChildMailboxCodeIgnoresStyleNoise(t *testing.T) {
	message := map[string]any{
		"html": `<style>.banner:after { content: "verification code 123456"; }</style><p>Welcome to OpenAI</p>`,
	}
	require.Empty(t, extractTeamMailboxVerificationCode(teamMailboxReadableText(message)))

	message["html"] = `<style>.banner { color: red; }</style><p>Your verification code is 654321</p>`
	require.Equal(t, "654321", extractTeamMailboxVerificationCode(teamMailboxReadableText(message)))

	message = map[string]any{
		"body": `<style>.banner:after { content: "verification code 222222"; }</style><p>Welcome</p>`,
	}
	require.Empty(t, extractTeamMailboxVerificationCode(teamMailboxReadableText(message)))
}

func TestTeamChildMailboxExpiredSessionIsRejectedAndCanBeDeleted(t *testing.T) {
	handler := &OpenAIOAuthHandler{teamMailboxStore: newOpenAITeamMailboxStore()}
	now := time.Now()
	handler.teamMailboxStore.now = func() time.Time { return now }
	handler.teamMailboxStore.sessions["expired"] = openAITeamMailboxSession{expiresAt: now.Add(-time.Second)}
	handler.teamMailboxStore.sessions["active"] = openAITeamMailboxSession{expiresAt: now.Add(time.Minute)}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	router.GET("/mailboxes/:session_id/code", handler.PollTeamChildMailboxCode)
	router.DELETE("/mailboxes/:session_id", handler.DeleteTeamChildMailboxSession)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mailboxes/expired/code", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/mailboxes/active", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	_, ok := handler.lookupTeamMailboxSession("active")
	require.False(t, ok)
}

func TestTeamMailboxQueryKeyAuthAddsKeyToCreationRequest(t *testing.T) {
	t.Setenv("TEAM_CHILD_MAIL_API_BASE", "https://mail.example.test")
	t.Setenv("TEAM_CHILD_MAIL_AUTH_MODE", "query-key")
	t.Setenv("TEAM_CHILD_MAIL_API_KEY", "query-secret")
	config, err := loadTeamMailboxProviderConfig(nil)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://mail.example.test/api/new_address", strings.NewReader(`{}`))
	applyTeamMailboxCreateAuth(req, config)
	require.Equal(t, "query-secret", req.URL.Query().Get("key"))
}

func TestImportTeamChildMailboxConfigStoresSecretServerSide(t *testing.T) {
	configPath := t.TempDir() + "/team-child-mail.env"
	t.Setenv("TEAM_CHILD_MAIL_CONFIG_FILE", configPath)
	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{teamMailboxStore: newOpenAITeamMailboxStore()}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	router.POST("/config", handler.ImportTeamChildMailboxConfig)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "config.json")
	require.NoError(t, err)
	_, err = part.Write([]byte(`{"cloudflare_api_base":"https://mail.example.test","cloudflare_api_key":"server-secret","cloudflare_auth_mode":"x-admin-auth","cloudflare_path_accounts":"/admin/new_address","cloudflare_path_messages":"/api/mails","defaultDomains":"example.test"}`))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/config", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "server-secret")
	stored, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.Contains(t, string(stored), `TEAM_CHILD_MAIL_API_KEY="server-secret"`)
	require.Equal(t, os.FileMode(0o600), func() os.FileMode { info, _ := os.Stat(configPath); return info.Mode().Perm() }())

	loaded, err := loadTeamMailboxProviderConfig(nil)
	require.NoError(t, err)
	require.Equal(t, "https://mail.example.test", loaded.baseURL.String())
	require.Equal(t, "server-secret", loaded.apiKey)
}

func TestTeamChildMailboxRedisStoreEnforcesOwnerAcrossHandlers(t *testing.T) {
	miniRedis := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: miniRedis.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	baseURL, err := url.Parse("https://mail.example.test")
	require.NoError(t, err)

	first := newOpenAITeamMailboxStore()
	first.configureRedis(client)
	second := newOpenAITeamMailboxStore()
	second.configureRedis(client)
	firstHandler := &OpenAIOAuthHandler{teamMailboxStore: first}
	secondHandler := &OpenAIOAuthHandler{teamMailboxStore: second}
	session := openAITeamMailboxSession{
		adminUserID: 42,
		email:       "one@example.test",
		token:       "mailbox-token",
		config: teamMailboxProviderConfig{
			baseURL: baseURL,
		},
		expiresAt: time.Now().Add(time.Minute),
	}
	require.NoError(t, first.create(t.Context(), "mailbox-session", session))

	loaded, ok, err := secondHandler.lookupTeamMailboxSessionContext(t.Context(), "mailbox-session", 7)
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, loaded.token)

	loaded, ok, err = secondHandler.lookupTeamMailboxSessionContext(t.Context(), "mailbox-session", 42)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "mailbox-token", loaded.token)
	require.NoError(t, second.delete(t.Context(), "mailbox-session", 7))
	_, ok, err = firstHandler.lookupTeamMailboxSessionContext(t.Context(), "mailbox-session", 42)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, second.delete(t.Context(), "mailbox-session", 42))
	_, ok, err = firstHandler.lookupTeamMailboxSessionContext(t.Context(), "mailbox-session", 42)
	require.NoError(t, err)
	require.False(t, ok)
}
