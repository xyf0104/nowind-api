//go:build unit

package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRestrictExecutionNodeProxyFallbackKeepsSameNodeOnly(t *testing.T) {
	apiProxy := int64(84)
	api2Proxy := int64(83)
	policy := executionNodeProxyFallbackPolicy{
		enabled: true,
		valid:   true,
		nodeByProxyID: map[int64]string{
			84: "api",
			83: "api2",
		},
	}

	nodeID, target, allowed := restrictExecutionNodeProxyFallback(policy, apiProxy, &apiProxy, true)
	require.True(t, allowed)
	require.Equal(t, "api", nodeID)
	require.Equal(t, apiProxy, *target)

	nodeID, target, allowed = restrictExecutionNodeProxyFallback(policy, apiProxy, &api2Proxy, true)
	require.False(t, allowed)
	require.Empty(t, nodeID)
	require.Nil(t, target)
}

func TestRestrictExecutionNodeProxyFallbackRejectsDirectAndInvalidModes(t *testing.T) {
	proxyID := int64(84)
	policy := executionNodeProxyFallbackPolicy{
		enabled:       true,
		valid:         true,
		nodeByProxyID: map[int64]string{84: "api"},
	}

	_, target, allowed := restrictExecutionNodeProxyFallback(policy, proxyID, nil, true)
	require.False(t, allowed)
	require.Nil(t, target)

	_, target, allowed = restrictExecutionNodeProxyFallback(
		executionNodeProxyFallbackPolicy{enabled: true},
		proxyID,
		&proxyID,
		true,
	)
	require.False(t, allowed)
	require.Nil(t, target)
}

func TestRestrictExecutionNodeProxyFallbackLeavesSingleNodePolicyUntouched(t *testing.T) {
	proxyID := int64(84)
	targetID := int64(91)
	_, target, allowed := restrictExecutionNodeProxyFallback(
		executionNodeProxyFallbackPolicy{},
		proxyID,
		&targetID,
		true,
	)
	require.True(t, allowed)
	require.Equal(t, targetID, *target)
}
