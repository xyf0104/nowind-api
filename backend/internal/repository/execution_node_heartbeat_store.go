package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

var executionNodeHeartbeatReleaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

type executionNodeHeartbeatStore struct {
	rdb *redis.Client
}

func NewExecutionNodeHeartbeatStore(rdb *redis.Client) service.ExecutionNodeHeartbeatStore {
	if rdb == nil {
		return nil
	}
	return &executionNodeHeartbeatStore{rdb: rdb}
}

func (s *executionNodeHeartbeatStore) TouchExecutionNode(
	ctx context.Context,
	nodeID, owner string,
	ttl time.Duration,
) error {
	return s.rdb.Set(ctx, service.ExecutionNodeHeartbeatKey(nodeID), owner, ttl).Err()
}

func (s *executionNodeHeartbeatStore) ReleaseExecutionNode(ctx context.Context, nodeID, owner string) error {
	return executionNodeHeartbeatReleaseScript.Run(
		ctx,
		s.rdb,
		[]string{service.ExecutionNodeHeartbeatKey(nodeID)},
		owner,
	).Err()
}

func (s *executionNodeHeartbeatStore) HealthyExecutionNodes(
	ctx context.Context,
	nodeIDs []string,
) (map[string]bool, error) {
	result := make(map[string]bool, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return result, nil
	}
	keys := make([]string, len(nodeIDs))
	for i, nodeID := range nodeIDs {
		keys[i] = service.ExecutionNodeHeartbeatKey(nodeID)
	}
	values, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for i, nodeID := range nodeIDs {
		result[nodeID] = i < len(values) && values[i] != nil && strings.TrimSpace(fmt.Sprint(values[i])) != ""
	}
	return result, nil
}
