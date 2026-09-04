package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	executionNodeHeartbeatKeyPrefix = "xiass:execution_node:heartbeat:"
	executionNodeHeartbeatInterval  = 5 * time.Second
	executionNodeHeartbeatTTL       = 20 * time.Second
)

var executionNodeHeartbeatReleaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

// ExecutionNodeHealthReader exposes the shared liveness view used by account
// routing. Implementations must fail closed when the shared store is unavailable.
type ExecutionNodeHealthReader interface {
	HealthyExecutionNodes(ctx context.Context, nodeIDs []string) (map[string]bool, error)
}

// ExecutionNodeHeartbeatService publishes one short-lived heartbeat per XIASS
// application instance. A missing heartbeat is the only condition that permits
// emergency local-egress takeover; ordinary upstream or proxy errors continue
// through the existing request-level account failover path.
type ExecutionNodeHeartbeatService struct {
	rdb      *redis.Client
	cfg      *config.Config
	nodeID   string
	owner    string
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewExecutionNodeHeartbeatService(rdb *redis.Client, cfg *config.Config) *ExecutionNodeHeartbeatService {
	nodeID := ""
	if cfg != nil {
		nodeID = strings.TrimSpace(cfg.Gateway.ExecutionNode.ID)
	}
	return &ExecutionNodeHeartbeatService{
		rdb:    rdb,
		cfg:    cfg,
		nodeID: nodeID,
		owner:  uuid.NewString(),
		stopCh: make(chan struct{}),
	}
}

func (s *ExecutionNodeHeartbeatService) enabled() bool {
	return s != nil && s.rdb != nil && s.cfg != nil &&
		s.cfg.Gateway.ExecutionNode.Enabled && validExecutionNodeID(s.nodeID)
}

func (s *ExecutionNodeHeartbeatService) Start() {
	if !s.enabled() {
		return
	}
	s.touch()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(executionNodeHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.touch()
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *ExecutionNodeHeartbeatService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
	if !s.enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = executionNodeHeartbeatReleaseScript.Run(ctx, s.rdb, []string{s.key(s.nodeID)}, s.owner).Err()
}

func (s *ExecutionNodeHeartbeatService) touch() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.rdb.Set(ctx, s.key(s.nodeID), s.owner, executionNodeHeartbeatTTL).Err()
}

// ExecutionNodeHeartbeatKey is the shared Redis key contract used by the
// scheduler and readiness probes. Keeping it here prevents the public ingress
// health check from drifting away from the failover liveness source.
func ExecutionNodeHeartbeatKey(nodeID string) string {
	return executionNodeHeartbeatKeyPrefix + strings.TrimSpace(nodeID)
}

func (s *ExecutionNodeHeartbeatService) key(nodeID string) string {
	return ExecutionNodeHeartbeatKey(nodeID)
}

func (s *ExecutionNodeHeartbeatService) HealthyExecutionNodes(ctx context.Context, nodeIDs []string) (map[string]bool, error) {
	if !s.enabled() {
		return nil, errors.New("execution node heartbeat service is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	keys := make([]string, 0, len(nodeIDs))
	normalized := make([]string, 0, len(nodeIDs))
	seen := make(map[string]struct{}, len(nodeIDs))
	for _, raw := range nodeIDs {
		nodeID := strings.TrimSpace(raw)
		if !validExecutionNodeID(nodeID) {
			return nil, fmt.Errorf("invalid execution node id %q", raw)
		}
		if _, ok := seen[nodeID]; ok {
			continue
		}
		seen[nodeID] = struct{}{}
		normalized = append(normalized, nodeID)
		keys = append(keys, s.key(nodeID))
	}
	result := make(map[string]bool, len(normalized))
	if len(keys) == 0 {
		return result, nil
	}
	values, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for i, nodeID := range normalized {
		result[nodeID] = i < len(values) && values[i] != nil && strings.TrimSpace(fmt.Sprint(values[i])) != ""
	}
	return result, nil
}
