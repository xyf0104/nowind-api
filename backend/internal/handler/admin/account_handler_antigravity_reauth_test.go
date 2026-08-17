package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type antigravityReauthAdminStub struct {
	*stubAdminService
	applyCalls  int
	applyID     int64
	applyInput  service.AntigravityOAuthCredentialsInput
	updated     *service.Account
	ensureCalls int
}

func (s *antigravityReauthAdminStub) ApplyAntigravityOAuthCredentials(
	_ context.Context,
	id int64,
	input service.AntigravityOAuthCredentialsInput,
) (*service.Account, error) {
	s.applyCalls++
	s.applyID = id
	s.applyInput = input
	return s.updated, nil
}

func (s *antigravityReauthAdminStub) ClearAccountError(context.Context, int64) (*service.Account, error) {
	return s.updated, nil
}

func (s *antigravityReauthAdminStub) EnsureAntigravityPrivacy(context.Context, *service.Account) string {
	s.ensureCalls++
	return service.AntigravityPrivacySet
}

type antigravityReauthTokenCache struct {
	deletedKeys []string
}

func (*antigravityReauthTokenCache) GetAccessToken(context.Context, string) (string, error) {
	return "", nil
}

func (*antigravityReauthTokenCache) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}

func (c *antigravityReauthTokenCache) DeleteAccessToken(_ context.Context, key string) error {
	c.deletedKeys = append(c.deletedKeys, key)
	return nil
}

func (*antigravityReauthTokenCache) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

func (*antigravityReauthTokenCache) ReleaseRefreshLock(context.Context, string) error {
	return nil
}

func TestApplyOAuthCredentialsUsesDedicatedAntigravityPathAndInvalidatesOldAndNewKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldAccount := &service.Account{
		ID:       104,
		Platform: service.PlatformAntigravity,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusError,
		Credentials: map[string]any{
			"access_token": "old-access",
			"project_id":   "old-project",
		},
	}
	newAccount := &service.Account{
		ID:       104,
		Platform: service.PlatformAntigravity,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusActive,
		Credentials: map[string]any{
			"access_token":   "new-access",
			"refresh_token":  "new-refresh",
			"project_id":     "new-project",
			"_token_version": int64(2),
		},
		Extra: map[string]any{"privacy_mode": service.AntigravityPrivacySet},
	}
	base := newStubAdminService()
	base.getAccountResult = oldAccount
	adminService := &antigravityReauthAdminStub{stubAdminService: base, updated: newAccount}
	tokenCache := &antigravityReauthTokenCache{}
	invalidator := service.NewCompositeTokenCacheInvalidator(tokenCache)
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, invalidator)
	router := gin.New()
	router.POST("/accounts/:id/apply-oauth-credentials", handler.ApplyOAuthCredentials)
	request := httptest.NewRequest(http.MethodPost, "/accounts/104/apply-oauth-credentials", bytes.NewBufferString(`{
		"type":"oauth",
		"credentials":{
			"access_token":"new-access",
			"refresh_token":"new-refresh",
			"project_id":"new-project",
			"plan_type":"pro"
		},
		"extra":{"privacy_mode":"privacy_set"}
	}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, adminService.applyCalls)
	require.Equal(t, int64(104), adminService.applyID)
	require.Equal(t, "new-refresh", adminService.applyInput.Credentials["refresh_token"])
	require.Equal(t, "pro", adminService.applyInput.Credentials["plan_type"])
	require.Equal(t, service.AntigravityPrivacySet, adminService.applyInput.PrivacyMode)
	require.Zero(t, base.updateAccountCalls, "the generic full-document update must not run")
	require.Zero(t, base.updateAccountExtraCalls, "privacy is owned by the dedicated service path")
	require.Zero(t, adminService.ensureCalls, "a successful privacy result must not be repeated")
	require.Equal(t, []string{
		"ag:old-project",
		"ag:account:104",
		"ag:new-project",
		"ag:account:104",
	}, tokenCache.deletedKeys)
}
