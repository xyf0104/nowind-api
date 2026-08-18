//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type blockingAntigravityAccountRefresher struct {
	started chan<- struct{}
	release <-chan struct{}
	info    *service.AntigravityTokenInfo
	err     error
}

func (r *blockingAntigravityAccountRefresher) RefreshAccountToken(
	ctx context.Context,
	_ *service.Account,
) (*service.AntigravityTokenInfo, error) {
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.info, r.err
}

func (*blockingAntigravityAccountRefresher) BuildAccountCredentials(info *service.AntigravityTokenInfo) map[string]any {
	credentials := map[string]any{
		"access_token": info.AccessToken,
		"expires_at":   info.ExpiresAt,
	}
	if info.RefreshToken != "" {
		credentials["refresh_token"] = info.RefreshToken
	}
	if info.ProjectID != "" {
		credentials["project_id"] = info.ProjectID
	}
	return credentials
}

type antigravityManualRefreshAdminStub struct {
	*stubAdminService

	mu              sync.Mutex
	current         *service.Account
	batchAccounts   []*service.Account
	applyCalls      int
	ensureCalls     int
	clearErrorCalls int
}

func cloneAntigravityRefreshAccount(account *service.Account) *service.Account {
	if account == nil {
		return nil
	}
	clone := *account
	clone.Credentials = make(map[string]any, len(account.Credentials))
	for key, value := range account.Credentials {
		clone.Credentials[key] = value
	}
	if account.ProxyID != nil {
		proxyID := *account.ProxyID
		clone.ProxyID = &proxyID
	}
	return &clone
}

func (s *antigravityManualRefreshAdminStub) ApplyAntigravityOAuthRefreshIfUnchanged(
	_ context.Context,
	attemptedAccount *service.Account,
	credentials map[string]any,
) (*service.Account, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyCalls++
	if s.current == nil ||
		!reflect.DeepEqual(s.current.Credentials, attemptedAccount.Credentials) ||
		!reflect.DeepEqual(s.current.ProxyID, attemptedAccount.ProxyID) {
		return cloneAntigravityRefreshAccount(s.current), false, nil
	}
	s.current.Credentials = make(map[string]any, len(credentials))
	for key, value := range credentials {
		s.current.Credentials[key] = value
	}
	return cloneAntigravityRefreshAccount(s.current), true, nil
}

func (s *antigravityManualRefreshAdminStub) GetAccountsByIDs(_ context.Context, _ []int64) ([]*service.Account, error) {
	return s.batchAccounts, nil
}

func (s *antigravityManualRefreshAdminStub) EnsureAntigravityPrivacy(_ context.Context, _ *service.Account) string {
	s.mu.Lock()
	s.ensureCalls++
	s.mu.Unlock()
	return service.AntigravityPrivacySet
}

func (s *antigravityManualRefreshAdminStub) ClearAccountError(_ context.Context, _ int64) (*service.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearErrorCalls++
	return cloneAntigravityRefreshAccount(s.current), nil
}

func (s *antigravityManualRefreshAdminStub) reauthorize(credentials map[string]any, proxyID *int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current.Credentials = credentials
	s.current.ProxyID = proxyID
	s.current.Status = service.StatusActive
	s.current.Schedulable = true
}

type antigravityRefreshInvalidator struct {
	mu       sync.Mutex
	accounts []*service.Account
}

func (i *antigravityRefreshInvalidator) InvalidateToken(_ context.Context, account *service.Account) error {
	i.mu.Lock()
	i.accounts = append(i.accounts, cloneAntigravityRefreshAccount(account))
	i.mu.Unlock()
	return nil
}

func TestRefreshSingleAccountAntigravityLateResultCannotOverwriteReauthorization(t *testing.T) {
	proxyID := int64(31)
	current := &service.Account{
		ID:          104,
		Platform:    service.PlatformAntigravity,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		ProxyID:     &proxyID,
		Credentials: map[string]any{
			"access_token":   "old-access",
			"refresh_token":  "old-refresh",
			"project_id":     "old-project",
			"_token_version": int64(1),
		},
	}
	attempted := cloneAntigravityRefreshAccount(current)
	adminService := &antigravityManualRefreshAdminStub{
		stubAdminService: newStubAdminService(),
		current:          current,
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	refresher := &blockingAntigravityAccountRefresher{
		started: started,
		release: release,
		info: &service.AntigravityTokenInfo{
			AccessToken:  "late-access",
			RefreshToken: "late-refresh",
			ProjectID:    "late-project",
		},
	}
	invalidator := &antigravityRefreshInvalidator{}
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, invalidator)
	handler.antigravityOAuthService = refresher

	type result struct {
		account *service.Account
		warning string
		err     error
	}
	done := make(chan result, 1)
	go func() {
		account, warning, err := handler.refreshSingleAccount(context.Background(), attempted)
		done <- result{account: account, warning: warning, err: err}
	}()

	<-started
	adminService.reauthorize(map[string]any{
		"access_token":   "reauthorized-access",
		"refresh_token":  "reauthorized-refresh",
		"project_id":     "reauthorized-project",
		"_token_version": int64(2),
	}, &proxyID)
	close(release)
	got := <-done

	require.NoError(t, got.err)
	require.Empty(t, got.warning)
	require.Equal(t, "reauthorized-refresh", got.account.GetCredential("refresh_token"))
	require.Equal(t, "reauthorized-project", got.account.GetCredential("project_id"))
	require.Equal(t, 1, adminService.applyCalls)
	require.Zero(t, adminService.ensureCalls)
	require.Zero(t, adminService.clearErrorCalls)
	require.Empty(t, invalidator.accounts)
}

func TestBatchRefreshAntigravityUsesSameReauthorizationCAS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	current := &service.Account{
		ID:          105,
		Platform:    service.PlatformAntigravity,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":   "old-access",
			"refresh_token":  "old-refresh",
			"project_id":     "old-project",
			"_token_version": int64(1),
		},
	}
	attempted := cloneAntigravityRefreshAccount(current)
	adminService := &antigravityManualRefreshAdminStub{
		stubAdminService: newStubAdminService(),
		current:          current,
		batchAccounts:    []*service.Account{attempted},
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	refresher := &blockingAntigravityAccountRefresher{
		started: started,
		release: release,
		info: &service.AntigravityTokenInfo{
			AccessToken:  "late-access",
			RefreshToken: "late-refresh",
			ProjectID:    "late-project",
		},
	}
	invalidator := &antigravityRefreshInvalidator{}
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, invalidator)
	handler.antigravityOAuthService = refresher
	router := gin.New()
	router.POST("/accounts/batch-refresh", handler.BatchRefresh)
	body, err := json.Marshal(map[string]any{"account_ids": []int64{current.ID}})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/accounts/batch-refresh", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(recorder, request)
		close(done)
	}()

	<-started
	adminService.reauthorize(map[string]any{
		"access_token":   "reauthorized-access",
		"refresh_token":  "reauthorized-refresh",
		"project_id":     "reauthorized-project",
		"_token_version": int64(2),
	}, nil)
	close(release)
	<-done

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), `"success":1`)
	require.Equal(t, "reauthorized-refresh", adminService.current.GetCredential("refresh_token"))
	require.Equal(t, 1, adminService.applyCalls)
	require.Zero(t, adminService.ensureCalls)
	require.Empty(t, invalidator.accounts)
}

func TestRefreshSingleAccountAntigravityProjectMissingStillInvalidatesOldAndNewCacheKeys(t *testing.T) {
	current := &service.Account{
		ID:       106,
		Platform: service.PlatformAntigravity,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusActive,
		Credentials: map[string]any{
			"access_token":  "old-access",
			"refresh_token": "old-refresh",
			"project_id":    "retained-project",
		},
	}
	attempted := cloneAntigravityRefreshAccount(current)
	adminService := &antigravityManualRefreshAdminStub{
		stubAdminService: newStubAdminService(),
		current:          current,
	}
	invalidator := &antigravityRefreshInvalidator{}
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, invalidator)
	handler.antigravityOAuthService = &blockingAntigravityAccountRefresher{info: &service.AntigravityTokenInfo{
		AccessToken:      "new-access",
		RefreshToken:     "new-refresh",
		ProjectID:        "retained-project",
		ProjectIDMissing: true,
	}}

	updated, warning, err := handler.refreshSingleAccount(context.Background(), attempted)

	require.NoError(t, err)
	require.Equal(t, "missing_project_id_temporary", warning)
	require.Equal(t, "new-refresh", updated.GetCredential("refresh_token"))
	require.Equal(t, 2, len(invalidator.accounts), "both pre-refresh and durable cache keys must be invalidated")
	require.Equal(t, "old-refresh", invalidator.accounts[0].GetCredential("refresh_token"))
	require.Equal(t, "new-refresh", invalidator.accounts[1].GetCredential("refresh_token"))
	require.Equal(t, 1, adminService.ensureCalls)
}
