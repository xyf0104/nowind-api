//go:build unit

package admin

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type settingHandlerExecutionNodeHealth map[string]bool

func (h settingHandlerExecutionNodeHealth) HealthyExecutionNodes(_ context.Context, nodeIDs []string) (map[string]bool, error) {
	result := make(map[string]bool, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if healthy, ok := h[nodeID]; ok {
			result[nodeID] = healthy
		}
	}
	return result, nil
}

func newSecondarySettingHandler() (*SettingHandler, *settingHandlerRepoStub) {
	repo := &settingHandlerRepoStub{values: map[string]string{}}
	cfg := &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api2",
		LegacyUnassignedNodeID: "api",
	}
	svc := service.NewSettingService(repo, cfg)
	svc.SetExecutionNodeHealthReader(settingHandlerExecutionNodeHealth{"api": true, "api2": true})
	return NewSettingHandler(svc, nil, nil, nil, nil, nil, nil), repo
}

func TestUpdateSettingsSecondaryRejectsSharedConfiguration(t *testing.T) {
	handler, repo := newSecondarySettingHandler()
	recorder := doUpdateSettings(t, handler, map[string]any{"site_name": "must-not-change"}, nil)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "EXECUTION_NODE_ADMIN_READ_ONLY")
	require.NotEqual(t, "must-not-change", repo.values[service.SettingKeySiteName])
}

func TestUpdateSettingsSecondaryAllowsOnlyExecutionNodeControls(t *testing.T) {
	handler, repo := newSecondarySettingHandler()
	recorder := doUpdateSettings(t, handler, map[string]any{
		"execution_node_balancing_enabled": false,
		"execution_node_weights": map[string]float64{"api": 9, "api2": 1},
		"execution_node_proxy_ids": map[string]int64{"api": 84, "api2": 85},
	}, nil)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.JSONEq(t, `{"api":9,"api2":1}`, repo.values[service.SettingKeyExecutionNodeWeights])
	require.JSONEq(t, `{"api":84,"api2":85}`, repo.values[service.SettingKeyExecutionNodeProxyIDs])
}

func TestUpdateSettingsSecondaryRejectsMixedExecutionAndSharedPayload(t *testing.T) {
	handler, repo := newSecondarySettingHandler()
	recorder := doUpdateSettings(t, handler, map[string]any{
		"execution_node_weights": map[string]float64{"api": 8, "api2": 2},
		"site_name": "must-not-change",
	}, nil)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.NotEqual(t, "must-not-change", repo.values[service.SettingKeySiteName])
	require.Empty(t, repo.values[service.SettingKeyExecutionNodeWeights], "a rejected mixed payload must not partially save weights")
}
