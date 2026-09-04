package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestExecutionNodeHeartbeatPublishesAndRemovesOwnedLease(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Enabled = true
	cfg.Gateway.ExecutionNode.ID = "api2"
	svc := NewExecutionNodeHeartbeatService(rdb, cfg)

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
