//go:build unit

package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type executionNodePairingHandlerRepo struct {
	mu     sync.Mutex
	values map[string]string
}

func newExecutionNodePairingHandlerRepo() *executionNodePairingHandlerRepo {
	return &executionNodePairingHandlerRepo{values: map[string]string{
		service.SettingKeyExecutionNodeClusterID: "shared-handler-database",
	}}
}

func (r *executionNodePairingHandlerRepo) Get(ctx context.Context, key string) (*service.Setting, error) {
	value, err := r.GetValue(ctx, key)
	if err != nil {
		return nil, err
	}
	return &service.Setting{Key: key, Value: value}, nil
}

func (r *executionNodePairingHandlerRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (r *executionNodePairingHandlerRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func (r *executionNodePairingHandlerRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
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

func (r *executionNodePairingHandlerRepo) SetMultiple(_ context.Context, values map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func (r *executionNodePairingHandlerRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]string, len(r.values))
	for key, value := range r.values {
		result[key] = value
	}
	return result, nil
}

func (r *executionNodePairingHandlerRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

func (r *executionNodePairingHandlerRepo) EnsureExecutionNodeClusterID(_ context.Context, candidate string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(r.values[service.SettingKeyExecutionNodeClusterID]) == "" {
		r.values[service.SettingKeyExecutionNodeClusterID] = candidate
	}
	return r.values[service.SettingKeyExecutionNodeClusterID], nil
}

func (r *executionNodePairingHandlerRepo) AcceptExecutionNodePairing(_ context.Context, expectedInvite string, peerSettings map[string]string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values[service.SettingKeyExecutionNodePairingInvite] != expectedInvite {
		return false, nil
	}
	r.values[service.SettingKeyExecutionNodePairingInvite] = ""
	for key, value := range peerSettings {
		r.values[key] = value
	}
	return true, nil
}

type executionNodePairingHandlerState string

func (s executionNodePairingHandlerState) EnsureSharedStateIdentity(context.Context, string) (string, error) {
	return string(s), nil
}

func newExecutionNodePairingHandlerService(nodeID string, repo *executionNodePairingHandlerRepo) *service.SettingService {
	cfg := &config.Config{
		JWT:  config.JWTConfig{Secret: "shared-handler-jwt-secret"},
		Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32)},
	}
	cfg.Gateway.ExecutionNode.Enabled = true
	cfg.Gateway.ExecutionNode.ID = nodeID
	svc := service.NewSettingService(repo, cfg)
	svc.SetVersion("1.1.74")
	svc.SetExecutionNodePairingStateReader(executionNodePairingHandlerState("shared-handler-redis"))
	return svc
}

func setupExecutionNodePairingHandlerRouter(svc *service.SettingService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/internal/execution-nodes/pair", NewExecutionNodePairingHandler(svc).Accept)
	return router
}

func TestExecutionNodePairingHandlerRejectsMissingInvite(t *testing.T) {
	router := setupExecutionNodePairingHandlerRouter(nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/internal/execution-nodes/pair", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), "pairing invite is required")
}

func TestExecutionNodePairingHandlerRejectsInvalidAndOversizedJSON(t *testing.T) {
	router := setupExecutionNodePairingHandlerRouter(nil)
	for name, body := range map[string][]byte{
		"invalid":   []byte(`{"node_id":`),
		"oversized": append([]byte(`{"padding":"`), bytes.Repeat([]byte("a"), 70*1024)...),
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/internal/execution-nodes/pair", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-XIASS-Execution-Node-Invite", strings.Repeat("a", 64))

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "invalid pairing request")
		})
	}
}

func TestExecutionNodePairingHandlerReturnsSuccessfulEnvelope(t *testing.T) {
	repo := newExecutionNodePairingHandlerRepo()
	receiver := newExecutionNodePairingHandlerService("api2", repo)
	invite, err := receiver.GenerateExecutionNodePairingInvite(context.Background())
	require.NoError(t, err)

	server := httptest.NewServer(setupExecutionNodePairingHandlerRouter(receiver))
	defer server.Close()

	initiator := newExecutionNodePairingHandlerService("api", repo)
	status, err := initiator.PairExecutionNode(context.Background(), server.URL, invite.Token)

	require.NoError(t, err)
	require.True(t, status.Paired)
	require.True(t, status.ProductionReady)
	require.Equal(t, "api2", status.Peer.NodeID)
	storedInvite, err := repo.GetValue(context.Background(), service.SettingKeyExecutionNodePairingInvite)
	require.NoError(t, err)
	require.Empty(t, storedInvite)
}
