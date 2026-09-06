//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// Two disposable, independent Redis servers and a synthetic Sentinel exercise
// discovery changes only. There is deliberately no replication or election;
// this test makes no claim about fencing or preservation of acknowledged writes.
func TestRedisSentinelSyntheticFailoverIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	startRedis := func() (string, *redis.Client) {
		t.Helper()
		ctr, err := tcredis.Run(ctx, "redis:8.4-alpine",
			testcontainers.WithCmdArgs("--requirepass", "data-secret", "--save", "", "--appendonly", "no"),
			testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
				hc.PortBindings = nat.PortMap{"6379/tcp": {{HostIP: "127.0.0.1"}}}
			}),
		)
		if ctr != nil {
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cleanupCancel()
				require.NoError(t, ctr.Terminate(cleanupCtx))
			})
		}
		require.NoError(t, err)
		addr, err := ctr.PortEndpoint(ctx, "6379/tcp", "")
		require.NoError(t, err)
		direct := redis.NewClient(&redis.Options{Addr: addr, Password: "data-secret", DB: 2})
		t.Cleanup(func() { _ = direct.Close() })
		require.NoError(t, direct.Ping(ctx).Err())
		return addr, direct
	}
	oldAddr, oldMaster := startRedis()
	newAddr, newMaster := startRedis()
	sentinel, switchMaster := syntheticRedisSentinel(t, oldAddr, nil)
	sentinel.RequireAuth("sentinel-secret")
	cfg := redisDiscoveryTestConfig()
	cfg.Redis.Host = "unused-standalone.example.invalid"
	cfg.Redis.Username = ""
	cfg.Redis.SentinelAddrs = []string{sentinel.Addr()}
	cfg.Redis.SentinelMasterName = "cache-primary"
	cfg.Redis.SentinelPassword = "sentinel-secret"
	client := InitRedis(cfg)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Set(ctx, "before-switch", "old-master", 0).Err())
	require.Equal(t, "old-master", oldMaster.Get(ctx, "before-switch").Val())
	require.ErrorIs(t, newMaster.Get(ctx, "before-switch").Err(), redis.Nil)

	// Wait for the library's subscription, then drive only a discovery event.
	require.Eventually(t, func() bool { return switchMaster(newAddr) > 0 }, 5*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		return client.Set(ctx, "after-switch", "new-master", 0).Err() == nil &&
			newMaster.Get(ctx, "after-switch").Val() == "new-master"
	}, 10*time.Second, 20*time.Millisecond)
	// Keep the old server reachable: subsequent requests must stay on the new one.
	for i := 0; i < 5; i++ {
		require.NoError(t, client.Incr(ctx, "new-master-only").Err())
	}
	require.Equal(t, "5", newMaster.Get(ctx, "new-master-only").Val())
	require.ErrorIs(t, oldMaster.Get(ctx, "new-master-only").Err(), redis.Nil)
	require.Equal(t, "old-master", oldMaster.Get(ctx, "before-switch").Val())

	// A fresh connection must also discover the new endpoint without old state.
	fresh := InitRedis(cfg)
	t.Cleanup(func() { _ = fresh.Close() })
	require.Equal(t, "new-master", fresh.Get(ctx, "after-switch").Val())
}
