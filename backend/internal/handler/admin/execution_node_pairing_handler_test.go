//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type executionNodePairingAdminRepo struct {
	mu     sync.Mutex
	values map[string]string
}

func newExecutionNodePairingAdminRepo() *executionNodePairingAdminRepo {
	return &executionNodePairingAdminRepo{values: map[string]string{
		service.SettingKeyExecutionNodeClusterID: "admin-handler-database",
	}}
}

func (r *executionNodePairingAdminRepo) Get(ctx context.Context, key string) (*service.Setting, error) {
	value, err := r.GetValue(ctx, key)
	if err != nil {
		return nil, err
	}
	return &service.Setting{Key: key, Value: value}, nil
}

func (r *executionNodePairingAdminRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (r *executionNodePairingAdminRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func (r *executionNodePairingAdminRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (r *executionNodePairingAdminRepo) SetMultiple(_ context.Context, values map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func (r *executionNodePairingAdminRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]string, len(r.values))
	for key, value := range r.values {
		result[key] = value
	}
	return result, nil
}

func (r *executionNodePairingAdminRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

type executionNodePairingAdminState string

func (s executionNodePairingAdminState) EnsureSharedStateIdentity(context.Context, string) (string, error) {
	return string(s), nil
}

func newExecutionNodePairingAdminHandler() (*SettingHandler, *executionNodePairingAdminRepo) {
	repo := newExecutionNodePairingAdminRepo()
	cfg := &config.Config{
		JWT:  config.JWTConfig{Secret: "admin-handler-jwt-secret"},
		Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32)},
	}
	cfg.Gateway.ExecutionNode.Enabled = true
	cfg.Gateway.ExecutionNode.ID = "api"
	svc := service.NewSettingService(repo, cfg)
	svc.SetExecutionNodePairingStateReader(executionNodePairingAdminState("admin-handler-redis"))
	return NewSettingHandler(svc, nil, nil, nil, nil, nil, nil), repo
}

func executeExecutionNodePairingAdminRequest(t *testing.T, method, path, body string, fn gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	fn(ctx)
	return recorder
}

func TestExecutionNodePairingAdminEndpoints(t *testing.T) {
	handler, repo := newExecutionNodePairingAdminHandler()

	inviteRecorder := executeExecutionNodePairingAdminRequest(t, http.MethodPost, "/api/v1/admin/settings/execution-nodes/pairing/invite", "", handler.GenerateExecutionNodePairingInvite)
	require.Equal(t, http.StatusOK, inviteRecorder.Code)
	var inviteResponse response.Response
	require.NoError(t, json.Unmarshal(inviteRecorder.Body.Bytes(), &inviteResponse))
	inviteData, ok := inviteResponse.Data.(map[string]any)
	require.True(t, ok)
	require.Len(t, inviteData["token"], 64)
	require.NotContains(t, repo.values[service.SettingKeyExecutionNodePairingInvite], inviteData["token"])

	statusRecorder := executeExecutionNodePairingAdminRequest(t, http.MethodGet, "/api/v1/admin/settings/execution-nodes/pairing", "", handler.GetExecutionNodePairingStatus)
	require.Equal(t, http.StatusOK, statusRecorder.Code)
	require.Contains(t, statusRecorder.Body.String(), `"invite_active":true`)

	invalidRecorder := executeExecutionNodePairingAdminRequest(t, http.MethodPost, "/api/v1/admin/settings/execution-nodes/pairing/join", `{"peer_url":`, handler.PairExecutionNode)
	require.Equal(t, http.StatusBadRequest, invalidRecorder.Code)

	unpairRecorder := executeExecutionNodePairingAdminRequest(t, http.MethodPost, "/api/v1/admin/settings/execution-nodes/pairing/unpair", "", handler.UnpairExecutionNode)
	require.Equal(t, http.StatusOK, unpairRecorder.Code)
	require.Contains(t, unpairRecorder.Body.String(), `"paired":false`)
}
