//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func newGatewayExecutionNodeStickyTestService(
	t *testing.T,
	accounts []*Account,
	cache *mockGatewayCacheForPlatform,
	groupRepo GroupRepository,
) *GatewayService {
	t.Helper()

	repo := &mockAccountRepoForPlatform{
		accounts:     make([]Account, 0, len(accounts)),
		accountsByID: make(map[int64]*Account, len(accounts)),
	}
	for _, account := range accounts {
		repo.accounts = append(repo.accounts, *account)
		repo.accountsByID[account.ID] = account
	}

	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api",
		DefaultProxyID:         84,
		LegacyUnassignedNodeID: "api",
	}
	settingService := NewSettingService(&executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":1,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}, cfg)

	return &GatewayService{
		accountRepo:        repo,
		groupRepo:          groupRepo,
		cache:              cache,
		cfg:                cfg,
		settingService:     settingService,
		concurrencyService: NewConcurrencyService(&mockConcurrencyCache{}),
	}
}

func gatewayExecutionNodeStickyTestAccounts() (*Account, *Account) {
	invalidSticky := executionNodeTestAccount(71, "api2", 1)
	invalidSticky.Platform = PlatformAnthropic
	invalidSticky.Status = StatusActive
	invalidSticky.Schedulable = true
	invalidSticky.Concurrency = 2
	invalidSticky.Proxy.Status = StatusDisabled

	validFallback := executionNodeTestAccount(72, "api", 1)
	validFallback.Platform = PlatformAnthropic
	validFallback.Status = StatusActive
	validFallback.Schedulable = true
	validFallback.Concurrency = 2

	return invalidSticky, validFallback
}

func TestGatewayStickySessionClearsInvalidExecutionNodeEgressWithoutModelRouting(t *testing.T) {
	ctx := context.Background()
	invalidSticky, validFallback := gatewayExecutionNodeStickyTestAccounts()
	cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"egress-sticky": invalidSticky.ID}}
	svc := newGatewayExecutionNodeStickyTestService(t, []*Account{invalidSticky, validFallback}, cache, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "egress-sticky", "claude-3-5-sonnet-20241022", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, validFallback.ID, selection.Account.ID)
	require.Equal(t, 1, cache.deletedSessions["egress-sticky"])
	require.Equal(t, validFallback.ID, cache.sessionBindings["egress-sticky"])
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestGatewayRoutedStickySessionClearsInvalidExecutionNodeEgress(t *testing.T) {
	ctx := context.Background()
	groupID := int64(73)
	invalidSticky, validFallback := gatewayExecutionNodeStickyTestAccounts()
	cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"routed-egress-sticky": invalidSticky.ID}}
	groupRepo := &mockGroupRepoForGateway{groups: map[int64]*Group{
		groupID: {
			ID:                  groupID,
			Platform:            PlatformAnthropic,
			Status:              StatusActive,
			Hydrated:            true,
			ModelRoutingEnabled: true,
			ModelRouting: map[string][]int64{
				"claude-3-5-sonnet-20241022": {invalidSticky.ID, validFallback.ID},
			},
		},
	}}
	svc := newGatewayExecutionNodeStickyTestService(t, []*Account{invalidSticky, validFallback}, cache, groupRepo)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "routed-egress-sticky", "claude-3-5-sonnet-20241022", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, validFallback.ID, selection.Account.ID)
	require.Equal(t, 1, cache.deletedSessions["routed-egress-sticky"])
	require.Equal(t, validFallback.ID, cache.sessionBindings["routed-egress-sticky"])
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestGatewayNewSessionSkipsInvalidHigherPriorityExecutionNodeEgress(t *testing.T) {
	ctx := context.Background()
	invalidPrimary, validFallback := gatewayExecutionNodeStickyTestAccounts()
	invalidPrimary.Priority = 1
	validFallback.Priority = 2
	cache := &mockGatewayCacheForPlatform{}
	svc := newGatewayExecutionNodeStickyTestService(t, []*Account{invalidPrimary, validFallback}, cache, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, validFallback.ID, selection.Account.ID)
	require.True(t, selection.Acquired)
	require.Nil(t, selection.WaitPlan)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}
