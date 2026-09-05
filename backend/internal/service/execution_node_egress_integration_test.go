package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// executionNodeEgressProxy is a real local HTTP forward proxy used by the
// integration tests below. It adds an unforgeable test-only marker after the
// request has crossed the selected proxy, which lets the upstream prove the
// actual egress instead of relying on the scheduler's metadata.
type executionNodeEgressProxy struct {
	nodeID    string
	proxyID   int64
	fail      atomic.Bool
	calls     atomic.Int64
	server    *httptest.Server
	transport *http.Transport
}

func newExecutionNodeEgressProxy(t *testing.T, nodeID string, proxyID int64) *executionNodeEgressProxy {
	t.Helper()
	proxy := &executionNodeEgressProxy{
		nodeID:    nodeID,
		proxyID:   proxyID,
		transport: &http.Transport{Proxy: nil},
	}
	proxy.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy.calls.Add(1)
		if proxy.fail.CompareAndSwap(true, false) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "test proxy failure")
			return
		}

		if r.URL == nil || !r.URL.IsAbs() {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "proxy requires an absolute-form target")
			return
		}
		outgoing, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, err.Error())
			return
		}
		outgoing.Header = r.Header.Clone()
		outgoing.Header.Set("X-XIASS-Test-Egress", proxy.nodeID)
		outgoing.Header.Set("X-XIASS-Test-Proxy-ID", strconv.FormatInt(proxy.proxyID, 10))

		response, err := proxy.transport.RoundTrip(outgoing)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, err.Error())
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	t.Cleanup(func() {
		proxy.server.Close()
		proxy.transport.CloseIdleConnections()
	})
	return proxy
}

func executionNodeEgressProxyURL(t *testing.T, proxy *executionNodeEgressProxy) *url.URL {
	t.Helper()
	parsed, err := url.Parse(proxy.server.URL)
	require.NoError(t, err)
	return parsed
}

func bindExecutionNodeTestProxy(t *testing.T, account *Account, proxy *executionNodeEgressProxy) {
	t.Helper()
	parsed := executionNodeEgressProxyURL(t, proxy)
	port, err := strconv.Atoi(parsed.Port())
	require.NoError(t, err)
	account.Proxy = &Proxy{
		ID:       *account.ProxyID,
		Protocol: parsed.Scheme,
		Host:     parsed.Hostname(),
		Port:     port,
		Status:   StatusActive,
	}
}

type executionNodeEgressObservation struct {
	accountID int64
	nodeID    string
	proxyID   string
	egress    string
}

func TestExecutionNodeSelectionUsesTheAccountOwnedEgress(t *testing.T) {
	var mu sync.Mutex
	var observations []executionNodeEgressObservation
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accountID, _ := strconv.ParseInt(r.Header.Get("X-XIASS-Test-Account-ID"), 10, 64)
		observation := executionNodeEgressObservation{
			accountID: accountID,
			nodeID:    r.Header.Get("X-XIASS-Test-Owner-Node"),
			proxyID:   r.Header.Get("X-XIASS-Test-Proxy-ID"),
			egress:    r.Header.Get("X-XIASS-Test-Egress"),
		}
		mu.Lock()
		observations = append(observations, observation)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(upstream.Close)

	apiProxy := newExecutionNodeEgressProxy(t, "api", 84)
	api2Proxy := newExecutionNodeEgressProxy(t, "api2", 83)
	api := executionNodeTestAccount(1001, "api", 1)
	api2 := executionNodeTestAccount(1002, "api2", 1)
	bindExecutionNodeTestProxy(t, api, apiProxy)
	bindExecutionNodeTestProxy(t, api2, api2Proxy)
	accounts := []*Account{api, api2}
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 1})
	clients := map[string]*http.Client{
		"api":  {Transport: &http.Transport{Proxy: http.ProxyURL(executionNodeEgressProxyURL(t, apiProxy))}},
		"api2": {Transport: &http.Transport{Proxy: http.ProxyURL(executionNodeEgressProxyURL(t, api2Proxy))}},
	}
	t.Cleanup(func() {
		for _, client := range clients {
			if transport, ok := client.Transport.(*http.Transport); ok {
				transport.CloseIdleConnections()
			}
		}
	})

	for i := 0; i < 80; i++ {
		anchor := fmt.Sprintf("actual-egress-%03d", i)
		ordered := orderExecutionNodeCandidates(accounts, func(account *Account) *Account { return account }, policy, anchor)
		require.Len(t, ordered, 2)
		selected := ordered[0]
		nodeID := selected.ExecutionNodeID(policy.legacyNodeID)
		require.Equal(t, policy.proxyIDs[nodeID], *selected.ProxyID)

		req, err := http.NewRequest(http.MethodGet, upstream.URL+"/probe", nil)
		require.NoError(t, err)
		req.Header.Set("X-XIASS-Test-Account-ID", strconv.FormatInt(selected.ID, 10))
		req.Header.Set("X-XIASS-Test-Owner-Node", nodeID)
		resp, err := clients[nodeID].Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())

		mu.Lock()
		observation := observations[len(observations)-1]
		mu.Unlock()
		t.Logf("call=%02d selected_account=%d owner_node=%s durable_proxy=%d actual_egress=%s", i+1, selected.ID, nodeID, *selected.ProxyID, observation.egress)
		require.Equal(t, selected.ID, observation.accountID)
		require.Equal(t, nodeID, observation.nodeID)
		require.Equal(t, nodeID, observation.egress)
		require.Equal(t, strconv.FormatInt(*selected.ProxyID, 10), observation.proxyID)
	}

	apiCalls := apiProxy.calls.Load()
	api2Calls := api2Proxy.calls.Load()
	require.Greater(t, apiCalls, int64(0), "weighted placement must use the api egress")
	require.Greater(t, api2Calls, int64(0), "weighted placement must use the api2 egress")
	mu.Lock()
	defer mu.Unlock()
	for _, observation := range observations {
		if observation.nodeID == "api" {
			require.Equal(t, "api", observation.egress)
		} else {
			require.Equal(t, "api2", observation.egress)
		}
	}
}

func TestOpenAISchedulerSelectionReachesTheSelectedAccountEgress(t *testing.T) {
	var mu sync.Mutex
	var observations []executionNodeEgressObservation
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accountID, _ := strconv.ParseInt(r.Header.Get("X-XIASS-Test-Account-ID"), 10, 64)
		observation := executionNodeEgressObservation{
			accountID: accountID,
			nodeID:    r.Header.Get("X-XIASS-Test-Owner-Node"),
			proxyID:   r.Header.Get("X-XIASS-Test-Proxy-ID"),
			egress:    r.Header.Get("X-XIASS-Test-Egress"),
		}
		mu.Lock()
		observations = append(observations, observation)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "scheduler-ok")
	}))
	t.Cleanup(upstream.Close)

	apiProxy := newExecutionNodeEgressProxy(t, "api", 84)
	api2Proxy := newExecutionNodeEgressProxy(t, "api2", 83)
	api := executionNodeTestAccount(1301, "api", 1)
	api2 := executionNodeTestAccount(1302, "api2", 1)
	bindExecutionNodeTestProxy(t, api, apiProxy)
	bindExecutionNodeTestProxy(t, api2, api2Proxy)
	const groupID int64 = 1300
	groupIDPtr := groupID
	for _, account := range []*Account{api, api2} {
		account.Platform = PlatformOpenAI
		account.Type = AccountTypeAPIKey
		account.Status = StatusActive
		account.Schedulable = true
		account.Concurrency = 1
		account.GroupIDs = []int64{groupID}
	}

	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api",
		DefaultProxyID:         84,
		LegacyUnassignedNodeID: "api",
	}
	settings := NewSettingService(&executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":1,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}, cfg)
	acquired := make([]int64, 0, 80)
	released := make([]int64, 0, 80)
	concurrency := NewConcurrencyService(schedulerTestConcurrencyCache{
		acquiredIDs: &acquired,
		releasedIDs: &released,
	})
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{*api, *api2}}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		settingService:     settings,
		concurrencyService: concurrency,
	}

	for i := 0; i < 80; i++ {
		selection, err := svc.selectAccountWithLoadAwareness(
			context.Background(), &groupIDPtr, PlatformOpenAI,
			fmt.Sprintf("scheduler-egress-%03d", i), "gpt-5.1", nil, false, "", true,
		)
		require.NoError(t, err)
		require.NotNil(t, selection)
		require.NotNil(t, selection.Account)
		selected := selection.Account
		nodeID := selected.ExecutionNodeID("api")
		require.Contains(t, []string{"api", "api2"}, nodeID)
		require.Equal(t, map[string]int64{"api": 84, "api2": 83}[nodeID], *selected.ProxyID)
		require.Equal(t, selected.Proxy.URL(), selected.requestProxyURL(), "the request must use the account's bound egress")

		proxyURL, err := url.Parse(selected.requestProxyURL())
		require.NoError(t, err)
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
		req, err := http.NewRequest(http.MethodGet, upstream.URL+"/scheduler", nil)
		require.NoError(t, err)
		req.Header.Set("X-XIASS-Test-Account-ID", strconv.FormatInt(selected.ID, 10))
		req.Header.Set("X-XIASS-Test-Owner-Node", nodeID)
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
		client.Transport.(*http.Transport).CloseIdleConnections()

		mu.Lock()
		observation := observations[len(observations)-1]
		mu.Unlock()
		t.Logf("scheduler_call=%02d selected_account=%d owner_node=%s durable_proxy=%d actual_egress=%s", i+1, selected.ID, nodeID, *selected.ProxyID, observation.egress)
		require.Equal(t, selected.ID, observation.accountID)
		require.Equal(t, nodeID, observation.nodeID)
		require.Equal(t, nodeID, observation.egress)
		require.Equal(t, strconv.FormatInt(*selected.ProxyID, 10), observation.proxyID)
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
	}

	require.Greater(t, apiProxy.calls.Load(), int64(0))
	require.Greater(t, api2Proxy.calls.Load(), int64(0))
	mu.Lock()
	defer mu.Unlock()
	for _, observation := range observations {
		require.Equal(t, observation.nodeID, observation.egress)
	}
	require.Len(t, acquired, 80)
	require.Len(t, released, 80)
}

func TestExecutionNodeStickySelectionKeepsTheSameEgress(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Header.Get("X-XIASS-Test-Egress"))
	}))
	t.Cleanup(upstream.Close)
	apiProxy := newExecutionNodeEgressProxy(t, "api", 84)
	api2Proxy := newExecutionNodeEgressProxy(t, "api2", 83)
	api := executionNodeTestAccount(1101, "api", 1)
	api2 := executionNodeTestAccount(1102, "api2", 1)
	bindExecutionNodeTestProxy(t, api, apiProxy)
	bindExecutionNodeTestProxy(t, api2, api2Proxy)
	accounts := []*Account{api, api2}
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 1})
	anchor := "sticky-egress-session"
	first := orderExecutionNodeCandidates(accounts, func(account *Account) *Account { return account }, policy, anchor)
	require.Len(t, first, 2)

	for attempt := 0; attempt < 3; attempt++ {
		ordered := orderExecutionNodeCandidates(accounts, func(account *Account) *Account { return account }, policy, anchor)
		require.Equal(t, first[0].ID, ordered[0].ID)
		selected := ordered[0]
		nodeID := selected.ExecutionNodeID(policy.legacyNodeID)
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(executionNodeEgressProxyURL(t, map[string]*executionNodeEgressProxy{
			"api":  apiProxy,
			"api2": api2Proxy,
		}[nodeID]))}}
		resp, err := client.Get(upstream.URL)
		require.NoError(t, err)
		body, readErr := io.ReadAll(resp.Body)
		require.NoError(t, readErr)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, nodeID, strings.TrimSpace(string(body)))
		client.Transport.(*http.Transport).CloseIdleConnections()
		t.Logf("sticky attempt=%d selected_account=%d owner_node=%s actual_egress=%s", attempt+1, selected.ID, nodeID, strings.TrimSpace(string(body)))
	}
}

func TestExecutionNodeFallbackUsesTheNextCandidateOwnEgress(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-XIASS-Test-Seen-Egress", r.Header.Get("X-XIASS-Test-Egress"))
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	apiProxy := newExecutionNodeEgressProxy(t, "api", 84)
	api2Proxy := newExecutionNodeEgressProxy(t, "api2", 83)
	api := executionNodeTestAccount(1201, "api", 1)
	api2 := executionNodeTestAccount(1202, "api2", 1)
	bindExecutionNodeTestProxy(t, api, apiProxy)
	bindExecutionNodeTestProxy(t, api2, api2Proxy)
	accounts := []*Account{api, api2}
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 1})
	ordered := orderExecutionNodeCandidates(accounts, func(account *Account) *Account { return account }, policy, "failover-egress-session")
	require.Len(t, ordered, 2)
	first, second := ordered[0], ordered[1]
	firstNode := first.ExecutionNodeID(policy.legacyNodeID)
	secondNode := second.ExecutionNodeID(policy.legacyNodeID)
	proxies := map[string]*executionNodeEgressProxy{"api": apiProxy, "api2": api2Proxy}
	proxies[firstNode].fail.Store(true)
	clients := map[string]*http.Client{
		"api":  {Transport: &http.Transport{Proxy: http.ProxyURL(executionNodeEgressProxyURL(t, apiProxy))}},
		"api2": {Transport: &http.Transport{Proxy: http.ProxyURL(executionNodeEgressProxyURL(t, api2Proxy))}},
	}
	t.Cleanup(func() {
		for _, client := range clients {
			client.Transport.(*http.Transport).CloseIdleConnections()
		}
	})

	firstReq, err := http.NewRequest(http.MethodGet, upstream.URL, nil)
	require.NoError(t, err)
	firstResp, err := clients[firstNode].Do(firstReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadGateway, firstResp.StatusCode)
	require.NoError(t, firstResp.Body.Close())

	secondReq, err := http.NewRequest(http.MethodGet, upstream.URL, nil)
	require.NoError(t, err)
	secondResp, err := clients[secondNode].Do(secondReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, secondResp.StatusCode)
	require.Equal(t, secondNode, secondResp.Header.Get("X-XIASS-Test-Seen-Egress"))
	require.NoError(t, secondResp.Body.Close())
	t.Logf("fallback first_account=%d first_node=%s first_status=%d next_account=%d next_node=%s next_actual_egress=%s", first.ID, firstNode, firstResp.StatusCode, second.ID, secondNode, secondResp.Header.Get("X-XIASS-Test-Seen-Egress"))
}
