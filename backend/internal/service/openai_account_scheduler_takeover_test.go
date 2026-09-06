package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

const openAITakeoverTestGroupID int64 = 8601

func openAITakeoverTestAccount(id int64, owner string, priority int) Account {
	account := executionNodeTestAccount(id, owner, priority)
	account.Platform, account.Type = PlatformOpenAI, AccountTypeAPIKey
	account.Status, account.Schedulable, account.Concurrency = StatusActive, true, 1
	account.GroupIDs = []int64{openAITakeoverTestGroupID}
	return *account
}

func openAITakeoverTestService(accounts []Account, concurrency schedulerTestConcurrencyCache, mode string) *OpenAIGatewayService {
	cfg := newSchedulerTestSubscriptionPriorityConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = mode != "legacy_no_batch"
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled: true, ID: "api2", DefaultProxyID: 83, LegacyUnassignedNodeID: "api", LegacyUnassignedProxyID: 84,
		EmergencyLocalEgress: true,
	}
	settings := NewSettingService(&executionNodeSettingRepo{}, cfg)
	settings.executionNodeRoutingCache.Store(&cachedExecutionNodeRoutingSettings{
		expiresAt: time.Now().Add(time.Hour).UnixNano(),
		settings: ExecutionNodeRoutingSettings{
			Available: true, Enabled: true, EmergencyLocalEgress: true,
			Weights: map[string]float64{"api": 9, "api2": 1}, ProxyIDs: map[string]int64{"api": 84, "api2": 83},
			Healthy: map[string]bool{"api": false, "api2": true}, LocalProxy: &Proxy{ID: 83, Status: StatusActive},
		},
	})
	return &OpenAIGatewayService{
		cfg: cfg, settingService: settings, cache: &schedulerTestGatewayCache{},
		accountRepo:        schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		concurrencyService: NewConcurrencyService(concurrency),
	}
}

func openAITakeoverTestSelect(svc *OpenAIGatewayService, mode, session string) (*AccountSelectionResult, error) {
	groupID := openAITakeoverTestGroupID
	if mode == "advanced" {
		selection, _, err := newDefaultOpenAIAccountScheduler(svc, nil).Select(context.Background(), OpenAIAccountScheduleRequest{
			GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-5.1", SessionHash: session,
			RequiredTransport: OpenAIUpstreamTransportAny,
		})
		return selection, err
	}
	return svc.selectAccountWithLoadAwareness(context.Background(), &groupID, PlatformOpenAI, session, "gpt-5.1", nil, false, "", false)
}

func TestOpenAIExecutionNodeTakeoverLocalSlotsBeforeRemotePriority(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		for _, scenario := range []string{"local_available", "local_full", "local_unavailable", "both_full", "stale_local_full", "lower_local_available", "load_read_error", "equal_priority_local_full"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				remote := openAITakeoverTestAccount(8611, "api", 0)
				local := openAITakeoverTestAccount(8612, "api2", 10)
				lowerLocal := openAITakeoverTestAccount(8613, "api2", 20)
				accounts := []Account{remote, local}
				acquires := []int64{}
				cache := schedulerTestConcurrencyCache{acquiredIDs: &acquires, acquireResults: map[int64]bool{remote.ID: true, local.ID: true}}
				want, acquired := local.ID, true
				switch scenario {
				case "local_full":
					cache.acquireResults[local.ID] = false
					want = remote.ID
				case "local_unavailable":
					accounts[1].Schedulable = false
					want = remote.ID
				case "both_full":
					cache.acquireResults[local.ID], cache.acquireResults[remote.ID] = false, false
					acquired = false
				case "stale_local_full":
					cache.loadMap = map[int64]*AccountLoadInfo{local.ID: {AccountID: local.ID, LoadRate: 100, CurrentConcurrency: 1}}
				case "lower_local_available":
					cache.acquireResults[local.ID] = false
					accounts = append(accounts, lowerLocal)
					want = lowerLocal.ID
				case "load_read_error":
					cache.loadBatchErr = errors.New("load snapshot unavailable")
				case "equal_priority_local_full":
					accounts[1].Priority = remote.Priority
					cache.acquireResults[local.ID] = false
					want = remote.ID
				}
				svc := openAITakeoverTestService(accounts, cache, mode)
				selection, err := openAITakeoverTestSelect(svc, mode, "")
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.Equal(t, want, selection.Account.ID)
				require.Equal(t, acquired, selection.Acquired)
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				if scenario != "local_unavailable" {
					require.NotEmpty(t, acquires)
					require.Equal(t, local.ID, acquires[0], "confirmed offline owner must not retain its old priority/9x placement share")
				}
				if acquired && want != remote.ID {
					require.NotContains(t, acquires, remote.ID, "do not acquire takeover capacity while an eligible healthy-owner slot can serve")
				}
				if want == remote.ID {
					require.Equal(t, int64(83), selection.Account.requestProxy().ID)
					require.Equal(t, int64(84), *selection.Account.ProxyID)
					require.Equal(t, "api", selection.Account.ExecutionNodeID("api"))
				}
				require.Equal(t, int64(84), *accounts[0].ProxyID)
				require.Nil(t, accounts[0].executionProxy)
			})
		}
	}
}

func TestOpenAIExecutionNodeTakeoverExhaustsHealthyTopK(t *testing.T) {
	remote := openAITakeoverTestAccount(8621, "api", 0)
	first := openAITakeoverTestAccount(8622, "api2", 10)
	second := openAITakeoverTestAccount(8623, "api2", 10)
	older, newer := time.Now().Add(-time.Hour), time.Now().Add(-time.Minute)
	first.LastUsedAt, second.LastUsedAt = &older, &newer
	acquires := []int64{}
	svc := openAITakeoverTestService([]Account{remote, first, second}, schedulerTestConcurrencyCache{
		acquiredIDs: &acquires, acquireResults: map[int64]bool{first.ID: false, second.ID: true, remote.ID: true},
	}, "advanced")
	selection, err := openAITakeoverTestSelect(svc, "advanced", "")
	require.NoError(t, err)
	require.Equal(t, second.ID, selection.Account.ID)
	require.Equal(t, []int64{first.ID, second.ID}, acquires, "Top-K=1 must not hide an available healthy-owner account before takeover")
	selection.ReleaseFunc()
}

func TestOpenAIExecutionNodeTakeoverOwnerRecoveryRestoresPriorityAndEgress(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		t.Run(mode, func(t *testing.T) {
			remote := openAITakeoverTestAccount(8631, "api", 0)
			local := openAITakeoverTestAccount(8632, "api2", 10)
			acquires := []int64{}
			results := map[int64]bool{local.ID: true, remote.ID: true}
			svc := openAITakeoverTestService([]Account{remote, local}, schedulerTestConcurrencyCache{acquiredIDs: &acquires, acquireResults: results}, mode)
			selection, err := openAITakeoverTestSelect(svc, mode, "")
			require.NoError(t, err)
			require.Equal(t, local.ID, selection.Account.ID)
			selection.ReleaseFunc()

			results[local.ID] = false
			selection, err = openAITakeoverTestSelect(svc, mode, "")
			require.NoError(t, err)
			require.Equal(t, remote.ID, selection.Account.ID)
			require.Equal(t, int64(83), selection.Account.requestProxy().ID)
			require.Equal(t, int64(84), *selection.Account.ProxyID)
			selection.ReleaseFunc()

			cached := svc.settingService.executionNodeRoutingCache.Load().(*cachedExecutionNodeRoutingSettings)
			recovered := cloneExecutionNodeRoutingSettings(cached.settings)
			recovered.Healthy["api"] = true
			svc.settingService.executionNodeRoutingCache.Store(&cachedExecutionNodeRoutingSettings{settings: recovered, expiresAt: cached.expiresAt})
			results[local.ID] = true
			acquires = nil
			selection, err = openAITakeoverTestSelect(svc, mode, "")
			require.NoError(t, err)
			require.Equal(t, remote.ID, selection.Account.ID, "healthy owner restores original hard priority")
			require.Equal(t, []int64{remote.ID}, acquires)
			require.Equal(t, int64(84), selection.Account.requestProxy().ID)
			require.Nil(t, selection.Account.executionProxy)
			require.Equal(t, "api", selection.Account.ExecutionNodeID("api"))
			selection.ReleaseFunc()
			require.Equal(t, int64(84), *remote.ProxyID)
			require.Nil(t, remote.executionProxy)
		})
	}
}

func TestOpenAIExecutionNodeTakeoverPreservesOrdinaryStickyAndScope(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		for _, invalidGroup := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/valid", true: "/other_group"}[invalidGroup], func(t *testing.T) {
				remote := openAITakeoverTestAccount(8641, "api", 0)
				local := openAITakeoverTestAccount(8642, "api2", 10)
				if invalidGroup {
					remote.GroupIDs = []int64{openAITakeoverTestGroupID + 1}
				}
				acquires := []int64{}
				svc := openAITakeoverTestService([]Account{remote, local}, schedulerTestConcurrencyCache{acquiredIDs: &acquires}, mode)
				cache := svc.cache.(*schedulerTestGatewayCache)
				cache.sessionBindings = map[string]int64{"openai:owned_sticky": remote.ID}
				selection, err := openAITakeoverTestSelect(svc, mode, "owned_sticky")
				require.NoError(t, err)
				if invalidGroup {
					require.Equal(t, local.ID, selection.Account.ID)
					require.NotContains(t, acquires, remote.ID, "emergency mode must not bypass the existing sticky/group boundary")
				} else {
					require.Equal(t, remote.ID, selection.Account.ID, "valid rolling sticky binding is not a new placement")
					require.Equal(t, remote.ID, cache.sessionBindings["openai:owned_sticky"])
					require.Equal(t, time.Minute, cache.refreshTTLs["openai:owned_sticky"])
					require.Zero(t, cache.deletedSessions["openai:owned_sticky"])
				}
				selection.ReleaseFunc()
			})
		}
	}
}

func TestOpenAIExecutionNodeTakeoverUnmovablePreviousResponseNeverSwitchesAccount(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(map[bool]string{false: "available", true: "busy"}[busy], func(t *testing.T) {
			remote := openAITakeoverTestAccount(8651, "api", 0)
			remote.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
			local := openAITakeoverTestAccount(8652, "api2", 10)
			acquires := []int64{}
			svc := openAITakeoverTestService([]Account{remote, local}, schedulerTestConcurrencyCache{
				acquiredIDs: &acquires, acquireResults: map[int64]bool{remote.ID: !busy, local.ID: true},
			}, "advanced")
			svc.cfg.Gateway.OpenAIWS = newOpenAIWSV2TestConfig().Gateway.OpenAIWS
			store := NewOpenAIWSStateStore(&stubGatewayCache{})
			svc.openaiWSStateStore = store
			groupID := openAITakeoverTestGroupID
			require.NoError(t, store.BindResponseAccount(context.Background(), groupID, "resp_offline_owner", remote.ID, time.Hour))
			selection, decision, err := newDefaultOpenAIAccountScheduler(svc, nil).Select(context.Background(), OpenAIAccountScheduleRequest{
				GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-5.1", RequiredTransport: OpenAIUpstreamTransportAny,
				PreviousResponseID: "resp_offline_owner", PreviousResponseCanMove: false, StickyWeighted: true,
			})
			require.NoError(t, err)
			require.Equal(t, remote.ID, selection.Account.ID)
			require.Equal(t, openAIAccountScheduleLayerPreviousResponse, decision.Layer)
			require.Equal(t, !busy, selection.Acquired)
			require.NotContains(t, acquires, local.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestOpenAIExecutionNodeTakeoverMovablePreviousHintCannotPreemptLocal(t *testing.T) {
	remote := openAITakeoverTestAccount(8661, "api", 0)
	local := openAITakeoverTestAccount(8662, "api2", 10)
	acquires := []int64{}
	svc := openAITakeoverTestService([]Account{remote, local}, schedulerTestConcurrencyCache{acquiredIDs: &acquires}, "advanced")
	groupID := openAITakeoverTestGroupID
	selection, _, err := newDefaultOpenAIAccountScheduler(svc, nil).Select(context.Background(), OpenAIAccountScheduleRequest{
		GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-5.1", RequiredTransport: OpenAIUpstreamTransportAny,
		PreviousResponseID: "resp_movable", PreviousResponseCanMove: true, StickyWeighted: true, StickyPreviousAccountID: remote.ID,
	})
	require.NoError(t, err)
	require.Equal(t, local.ID, selection.Account.ID)
	require.Equal(t, []int64{local.ID}, acquires)
	selection.ReleaseFunc()
}

type openAITakeoverUserAllowlistRepo struct {
	UserGroupAccountAllowlistRepository
	accountsByUser map[int64][]int64
}

func (r openAITakeoverUserAllowlistRepo) GetAllowedAccountIDs(_ context.Context, userID, _ int64) ([]int64, bool, error) {
	return r.accountsByUser[userID], true, nil
}

func TestOpenAIExecutionNodeTakeoverCannotExpandUserAccountScope(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		t.Run(mode, func(t *testing.T) {
			remote := openAITakeoverTestAccount(8671, "api", 0)
			local := openAITakeoverTestAccount(8672, "api2", 10)
			acquires := []int64{}
			svc := openAITakeoverTestService([]Account{remote, local}, schedulerTestConcurrencyCache{acquiredIDs: &acquires}, mode)
			policy := NewUserGroupAccountAllowlistPolicy(openAITakeoverUserAllowlistRepo{
				accountsByUser: map[int64][]int64{71: {remote.ID}, 72: {local.ID}},
			})
			svc.accountRepo = NewUserGroupAccountFilteringRepository(svc.accountRepo, policy)
			svc.SetAccountCandidateAccessPolicy(policy)
			groupID := openAITakeoverTestGroupID
			for _, userID := range []int64{71, 72, 71} {
				ctx := context.WithValue(context.Background(), ctxkey.UserID, userID)
				acquires = nil
				var selection *AccountSelectionResult
				var err error
				if mode == "advanced" {
					selection, _, err = newDefaultOpenAIAccountScheduler(svc, nil).Select(ctx, OpenAIAccountScheduleRequest{
						GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-5.1", RequiredTransport: OpenAIUpstreamTransportAny,
						SessionHash: "same_client_session_label",
					})
				} else {
					selection, err = svc.selectAccountWithLoadAwareness(ctx, &groupID, PlatformOpenAI, "same_client_session_label", "gpt-5.1", nil, false, "", false)
				}
				require.NoError(t, err)
				want := remote.ID
				if userID == 72 {
					want = local.ID
				}
				require.Equal(t, want, selection.Account.ID)
				require.Equal(t, []int64{want}, acquires, "local preference and cached sticky IDs must both stay inside the requesting user's allowed pool")
				selection.ReleaseFunc()
			}
		})
	}
}
