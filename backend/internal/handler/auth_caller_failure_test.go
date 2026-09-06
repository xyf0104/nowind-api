//go:build unit

package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const authCallerSensitiveDetail = "synthetic-sensitive-store-detail"

type authCallerHTTPStore struct {
	service.RefreshTokenCache
	storeErr    error
	userErr     error
	familyErr   error
	revokeErr   error
	calls       []string
	deletedHash string
	userIDs     []int64
}

func (s *authCallerHTTPStore) StoreRefreshToken(context.Context, string, *service.RefreshTokenData, time.Duration) error {
	s.calls = append(s.calls, "store")
	return s.storeErr
}

func (s *authCallerHTTPStore) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	s.calls = append(s.calls, "user_index")
	return s.userErr
}

func (s *authCallerHTTPStore) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	s.calls = append(s.calls, "family_index")
	return s.familyErr
}

func (s *authCallerHTTPStore) DeleteRefreshToken(_ context.Context, hash string) error {
	s.calls = append(s.calls, "revoke_hash")
	s.deletedHash = hash
	return s.revokeErr
}

func (s *authCallerHTTPStore) DeleteUserRefreshTokens(_ context.Context, userID int64) error {
	s.calls = append(s.calls, "revoke_user")
	s.userIDs = append(s.userIDs, userID)
	return s.revokeErr
}

type authCallerHTTPAdmissionStore struct {
	*authCallerHTTPStore
	ticket *service.RefreshTokenIssuance
	err    error
}

type authCallerHTTPGuardedStore struct {
	*authCallerHTTPStore
}

func (*authCallerHTTPGuardedStore) RequiresRefreshTokenIssuanceAdmission() bool { return true }

func (s *authCallerHTTPAdmissionStore) PrepareRefreshTokenIssuance(context.Context, int64) (*service.RefreshTokenIssuance, error) {
	s.calls = append(s.calls, "prepare")
	return s.ticket, s.err
}

type authCallerHTTPUserRepo struct {
	service.UserRepository
	user *service.User
	err  error
}

func (r *authCallerHTTPUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	if r.err != nil {
		return nil, r.err
	}
	user := *r.user
	return &user, nil
}

func newAuthCallerHTTPService(store service.RefreshTokenCache, repo service.UserRepository) *service.AuthService {
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "synthetic-caller-test-signing-key", ExpireHour: 168}}
	return service.NewAuthService(nil, repo, nil, store, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
}

func captureAuthCallerLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	logs := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

func TestAuthCallerTokenPairAdmissionAndLegacyFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	validTicket := &service.RefreshTokenIssuance{ID: "synthetic-admission", UserID: 17}
	sensitiveErr := infraerrors.ServiceUnavailable("SENSITIVE_BACKEND_REASON", authCallerSensitiveDetail)
	for _, tc := range []struct {
		name          string
		authoritative bool
		guarded       bool
		noStore       bool
		prepareErr    error
		storeErr      error
		userErr       error
		familyErr     error
		ticket        *service.RefreshTokenIssuance
		wantStatus    int
		wantRefresh   bool
		wantCalls     []string
	}{
		{name: "admission failure", authoritative: true, prepareErr: sensitiveErr, wantStatus: 503, wantCalls: []string{"prepare"}},
		{name: "missing admission", authoritative: true, wantStatus: 503, wantCalls: []string{"prepare"}},
		{name: "wrong user admission", authoritative: true, ticket: &service.RefreshTokenIssuance{UserID: 18}, wantStatus: 503, wantCalls: []string{"prepare"}},
		{name: "durable store failure", authoritative: true, ticket: validTicket, storeErr: sensitiveErr, wantStatus: 503, wantCalls: []string{"prepare", "store"}},
		{name: "admitted success", authoritative: true, ticket: validTicket, wantStatus: 200, wantRefresh: true, wantCalls: []string{"prepare", "store", "user_index", "family_index"}},
		{name: "legacy store fallback", storeErr: sensitiveErr, wantStatus: 200, wantCalls: []string{"store"}},
		{name: "legacy unconfigured fallback", noStore: true, wantStatus: 200},
		{name: "legacy pair success", wantStatus: 200, wantRefresh: true, wantCalls: []string{"store", "user_index", "family_index"}},
		{name: "migrating authority cannot bypass storage", guarded: true, storeErr: sensitiveErr, wantStatus: 503, wantCalls: []string{"store"}},
		{name: "guarded legacy success", guarded: true, wantStatus: 200, wantRefresh: true, wantCalls: []string{"store", "user_index", "family_index"}},
		{name: "authority changed during user index", guarded: true, userErr: sensitiveErr, wantStatus: 503, wantCalls: []string{"store", "user_index"}},
		{name: "authority changed during family index", guarded: true, familyErr: sensitiveErr, wantStatus: 503, wantCalls: []string{"store", "user_index", "family_index"}},
		{name: "durable membership verification fails", authoritative: true, ticket: validTicket, familyErr: sensitiveErr, wantStatus: 503, wantCalls: []string{"prepare", "store", "user_index", "family_index"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureAuthCallerLogs(t)
			store := &authCallerHTTPStore{storeErr: tc.storeErr, userErr: tc.userErr, familyErr: tc.familyErr}
			var cache service.RefreshTokenCache = store
			if tc.authoritative {
				cache = &authCallerHTTPAdmissionStore{authCallerHTTPStore: store, ticket: tc.ticket, err: tc.prepareErr}
			} else if tc.guarded {
				cache = &authCallerHTTPGuardedStore{authCallerHTTPStore: store}
			} else if tc.noStore {
				cache = nil
			}
			svc := newAuthCallerHTTPService(cache, nil)
			h := &AuthHandler{authService: svc}
			user := &service.User{ID: 17, Email: "synthetic@example.invalid", Role: service.RoleUser, Status: service.StatusActive}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
			h.respondWithTokenPair(c, user)

			require.Equal(t, tc.wantStatus, recorder.Code)
			require.Equal(t, tc.wantCalls, store.calls)
			require.NotContains(t, recorder.Body.String(), authCallerSensitiveDetail)
			require.NotContains(t, recorder.Body.String(), "SENSITIVE_BACKEND_REASON")
			require.NotContains(t, logs.String(), authCallerSensitiveDetail)
			var body struct {
				Code   int           `json:"code"`
				Reason string        `json:"reason"`
				Data   *AuthResponse `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			if tc.wantStatus != http.StatusOK {
				require.Equal(t, http.StatusServiceUnavailable, body.Code)
				require.Equal(t, "SERVICE_UNAVAILABLE", body.Reason)
				require.Nil(t, body.Data)
				require.NotContains(t, recorder.Body.String(), "access_token")
				require.NotContains(t, recorder.Body.String(), "refresh_token")
				return
			}
			require.Zero(t, body.Code)
			require.NotNil(t, body.Data)
			claims, err := svc.ValidateToken(body.Data.AccessToken)
			require.NoError(t, err)
			require.Equal(t, user.ID, claims.UserID)
			require.Equal(t, tc.wantRefresh, body.Data.RefreshToken != "")
		})
	}
}

func TestAuthCallerPasswordLoginHonorsAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name          string
		authoritative bool
		prepareFails  bool
		wantStatus    int
	}{
		{name: "admission failure", authoritative: true, prepareFails: true, wantStatus: 503},
		{name: "durable store failure", authoritative: true, wantStatus: 503},
		{name: "legacy fallback", wantStatus: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, client := newOAuthPendingFlowTestHandler(t, false)
			logs := captureAuthCallerLogs(t)
			password := "synthetic-login-password"
			hash, err := h.authService.HashPassword(password)
			require.NoError(t, err)
			user, err := client.User.Create().
				SetEmail("caller-login@example.invalid").SetPasswordHash(hash).
				SetRole(service.RoleUser).SetStatus(service.StatusActive).
				Save(context.Background())
			require.NoError(t, err)
			store := &authCallerHTTPStore{storeErr: errors.New(authCallerSensitiveDetail)}
			var cache service.RefreshTokenCache = store
			if tc.authoritative {
				admission := &authCallerHTTPAdmissionStore{authCallerHTTPStore: store,
					ticket: &service.RefreshTokenIssuance{ID: "synthetic-login-admission", UserID: user.ID}}
				if tc.prepareFails {
					admission.err = errors.New(authCallerSensitiveDetail)
				}
				cache = admission
			}
			cfg := &config.Config{JWT: config.JWTConfig{Secret: "synthetic-login-signing-key", ExpireHour: 168}}
			h.authService = service.NewAuthService(client, &oauthPendingFlowUserRepo{client: client}, nil,
				cache, cfg, h.settingSvc, nil, nil, nil, nil, nil, nil, nil)
			body, err := json.Marshal(LoginRequest{Email: user.Email, Password: password})
			require.NoError(t, err)
			router := gin.New()
			router.POST("/api/v1/auth/login", h.Login)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)

			require.Equal(t, tc.wantStatus, recorder.Code)
			if tc.prepareFails {
				require.Equal(t, []string{"prepare"}, store.calls)
			} else if tc.authoritative {
				require.Equal(t, []string{"prepare", "store"}, store.calls)
			} else {
				require.Equal(t, []string{"store"}, store.calls)
			}
			if tc.authoritative {
				require.NotContains(t, recorder.Body.String(), "access_token")
				require.NotContains(t, recorder.Body.String(), "refresh_token")
			} else {
				require.Contains(t, recorder.Body.String(), "access_token")
				require.NotContains(t, recorder.Body.String(), "refresh_token")
			}
			for _, sensitive := range []string{authCallerSensitiveDetail, password, hash} {
				require.NotContains(t, recorder.Body.String(), sensitive)
				require.NotContains(t, logs.String(), sensitive)
			}
		})
	}
}

func TestAuthCallerLogoutDoesNotAcknowledgeFailedRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		token      string
		err        error
		wantStatus int
		wantDelete bool
	}{
		{name: "durable failure", token: "rt_synthetic-logout", err: errors.New(authCallerSensitiveDetail), wantStatus: 503, wantDelete: true},
		{name: "canceled", token: "rt_synthetic-logout", err: context.Canceled, wantStatus: 503, wantDelete: true},
		{name: "committed", token: "rt_synthetic-logout", wantStatus: 200, wantDelete: true},
		{name: "invalid token", token: "invalid-synthetic-token", wantStatus: 401},
		{name: "empty body", wantStatus: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureAuthCallerLogs(t)
			store := &authCallerHTTPStore{revokeErr: tc.err}
			cache := &authCallerHTTPAdmissionStore{authCallerHTTPStore: store}
			h := &AuthHandler{authService: newAuthCallerHTTPService(cache, nil)}
			body := ""
			if tc.token != "" {
				encoded, err := json.Marshal(LogoutRequest{RefreshToken: tc.token})
				require.NoError(t, err)
				body = string(encoded)
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			h.Logout(c)

			require.Equal(t, tc.wantStatus, recorder.Code)
			if tc.wantDelete {
				hash := sha256.Sum256([]byte(tc.token))
				require.Equal(t, hex.EncodeToString(hash[:]), store.deletedHash)
				require.Equal(t, []string{"revoke_hash"}, store.calls)
			} else {
				require.Empty(t, store.calls)
			}
			cookie := findCookie(recorder.Result().Cookies(), oauthBindAccessTokenCookieName)
			require.NotNil(t, cookie, "local OAuth cleanup must still run on durable failure")
			require.Equal(t, -1, cookie.MaxAge)
			if tc.wantStatus != http.StatusOK {
				require.NotContains(t, recorder.Body.String(), "Logged out successfully")
			} else {
				require.Contains(t, recorder.Body.String(), "Logged out successfully")
			}
			require.NotContains(t, recorder.Body.String(), authCallerSensitiveDetail)
			require.NotContains(t, logs.String(), authCallerSensitiveDetail)
			if tc.token != "" {
				require.NotContains(t, logs.String(), tc.token)
				require.NotContains(t, recorder.Body.String(), tc.token)
			}
		})
	}
}

func TestAuthCallerLogoutFailureStillConsumesPendingOAuth(t *testing.T) {
	h, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()
	session, err := client.PendingAuthSession.Create().
		SetSessionToken("synthetic-failed-logout-pending").SetIntent("login").
		SetProviderType("oidc").SetProviderKey("https://issuer.example.invalid").
		SetProviderSubject("synthetic-subject").SetBrowserSessionKey("synthetic-browser").
		SetExpiresAt(time.Now().Add(time.Minute)).Save(ctx)
	require.NoError(t, err)
	store := &authCallerHTTPAdmissionStore{authCallerHTTPStore: &authCallerHTTPStore{revokeErr: errors.New(authCallerSensitiveDetail)}}
	h.authService = service.NewAuthService(client, nil, nil, store, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader(`{"refresh_token":"rt_synthetic"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.AddCookie(&http.Cookie{Name: oauthPendingSessionCookieName, Value: encodeCookieValue(session.SessionToken)})
	c.Request.AddCookie(&http.Cookie{Name: oauthPendingBrowserCookieName, Value: encodeCookieValue(session.BrowserSessionKey)})
	h.Logout(c)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	stored, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.ConsumedAt)
	for _, name := range []string{oauthPendingSessionCookieName, oauthPendingBrowserCookieName} {
		cookie := findCookie(recorder.Result().Cookies(), name)
		require.NotNil(t, cookie)
		require.Equal(t, -1, cookie.MaxAge)
	}
}

func TestAuthCallerRevokeAllDoesNotAcknowledgeFailedRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name         string
		storeErr     error
		lookupErr    error
		unauthorized bool
		wantStatus   int
		wantRevoke   bool
	}{
		{name: "durable failure", storeErr: errors.New(authCallerSensitiveDetail), wantStatus: 500, wantRevoke: true},
		{name: "lookup failure", lookupErr: errors.New(authCallerSensitiveDetail), wantStatus: 500},
		{name: "committed", wantStatus: 200, wantRevoke: true},
		{name: "unauthorized", unauthorized: true, wantStatus: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureAuthCallerLogs(t)
			user := &service.User{ID: 17, Email: "synthetic@example.invalid", TokenVersion: 7}
			before := *user
			repo := &authCallerHTTPUserRepo{user: user, err: tc.lookupErr}
			store := &authCallerHTTPStore{revokeErr: tc.storeErr}
			cache := &authCallerHTTPAdmissionStore{authCallerHTTPStore: store}
			h := &AuthHandler{authService: newAuthCallerHTTPService(cache, repo)}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/revoke-all-sessions", nil)
			if !tc.unauthorized {
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: user.ID})
			}
			h.RevokeAllSessions(c)

			require.Equal(t, tc.wantStatus, recorder.Code)
			if tc.wantRevoke {
				require.Equal(t, []int64{user.ID}, store.userIDs)
			} else {
				require.Empty(t, store.calls)
			}
			require.Equal(t, before, *user)
			if tc.wantStatus != http.StatusOK {
				require.NotContains(t, recorder.Body.String(), "All sessions have been revoked")
			}
			require.NotContains(t, recorder.Body.String(), authCallerSensitiveDetail)
			require.NotContains(t, logs.String(), authCallerSensitiveDetail)
		})
	}
}
