package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type memoryExecutionNodeHeartbeatStore struct {
	mu     sync.Mutex
	owners map[string]string
}

func (s *memoryExecutionNodeHeartbeatStore) TouchExecutionNode(
	_ context.Context,
	nodeID, owner string,
	_ time.Duration,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners == nil {
		s.owners = make(map[string]string)
	}
	s.owners[nodeID] = owner
	return nil
}

func (s *memoryExecutionNodeHeartbeatStore) ReleaseExecutionNode(_ context.Context, nodeID, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners[nodeID] == owner {
		delete(s.owners, nodeID)
	}
	return nil
}

func (s *memoryExecutionNodeHeartbeatStore) HealthyExecutionNodes(
	_ context.Context,
	nodeIDs []string,
) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]bool, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		result[nodeID] = s.owners[nodeID] != ""
	}
	return result, nil
}

func TestExecutionNodeHeartbeatPublishesAndRemovesOwnedLease(t *testing.T) {
	store := &memoryExecutionNodeHeartbeatStore{}
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Enabled = true
	cfg.Gateway.ExecutionNode.ID = "api2"
	svc := NewExecutionNodeHeartbeatService(store, cfg)

	svc.Start()
	healthy, err := svc.HealthyExecutionNodes(context.Background(), []string{"api", "api2"})
	require.NoError(t, err)
	require.False(t, healthy["api"])
	require.True(t, healthy["api2"])

	svc.Stop()
	healthy, err = svc.HealthyExecutionNodes(context.Background(), []string{"api2"})
	require.NoError(t, err)
	require.False(t, healthy["api2"])
}
