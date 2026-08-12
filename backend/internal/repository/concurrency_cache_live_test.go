package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestLiveLeaseReplacesRegularSlotsAndCountsTowardLimits(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	regular := NewConcurrencyCache(client, 15, 900)
	live, ok := regular.(service.LiveConcurrencyCache)
	require.True(t, ok)
	ctx := context.Background()

	accountAcquired, err := regular.AcquireAccountSlot(ctx, 10, 1, "regular-account")
	require.NoError(t, err)
	require.True(t, accountAcquired)
	userAcquired, err := regular.AcquireUserSlot(ctx, 20, 1, "regular-user")
	require.NoError(t, err)
	require.True(t, userAcquired)

	acquired, err := live.AcquireLiveLease(ctx, 10, 1, 20, 1, 30, "live-lease", true)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, regular.ReleaseAccountSlot(ctx, 10, "regular-account"))
	require.NoError(t, regular.ReleaseUserSlot(ctx, 20, "regular-user"))

	accountCount, err := regular.GetAccountConcurrency(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, accountCount)
	userCount, err := regular.GetUserConcurrency(ctx, 20)
	require.NoError(t, err)
	require.Equal(t, 1, userCount)
	accountAcquired, err = regular.AcquireAccountSlot(ctx, 10, 1, "ordinary-blocked")
	require.NoError(t, err)
	require.False(t, accountAcquired)

	refreshed, err := live.RefreshLiveLease(ctx, 10, 20, 30, "live-lease")
	require.NoError(t, err)
	require.True(t, refreshed)
	require.NoError(t, live.ReleaseLiveLease(ctx, 10, 20, 30, "live-lease"))
	accountAcquired, err = regular.AcquireAccountSlot(ctx, 10, 1, "ordinary-allowed")
	require.NoError(t, err)
	require.True(t, accountAcquired)
}

func TestLiveLeaseExpiresWithoutRefresh(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	regular := NewConcurrencyCache(client, 15, 900)
	live, ok := regular.(service.LiveConcurrencyCache)
	require.True(t, ok)
	ctx := context.Background()

	acquired, err := live.AcquireLiveLease(ctx, 10, 1, 20, 1, 30, "expired-live", false)
	require.NoError(t, err)
	require.True(t, acquired)

	redisServer.FastForward(61 * time.Second)
	acquired, err = regular.AcquireAccountSlot(ctx, 10, 1, "ordinary-after-expiry")
	require.NoError(t, err)
	require.True(t, acquired)
	refreshed, err := live.RefreshLiveLease(ctx, 10, 20, 30, "expired-live")
	require.NoError(t, err)
	require.False(t, refreshed)
}

func TestGroupAccountConcurrencySnapshotIsScopedAndRejectsLegacyMembers(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	ctx := context.Background()

	require.NoError(t, cache.TrackGroupSlot(ctx, 10, 101, "request-a"))
	require.NoError(t, cache.TrackGroupSlot(ctx, 10, 101, "request-b"))
	require.NoError(t, cache.TrackGroupSlot(ctx, 20, 101, "request-c"))
	require.NoError(t, cache.TrackGroupSlot(ctx, 10, 202, "request-d"))

	group10, complete, snapshotAt, err := cache.GetGroupAccountConcurrency(ctx, 10)
	require.NoError(t, err)
	require.True(t, complete)
	require.False(t, snapshotAt.IsZero())
	require.Equal(t, map[int64]int{101: 2, 202: 1}, group10)

	group20, complete, _, err := cache.GetGroupAccountConcurrency(ctx, 20)
	require.NoError(t, err)
	require.True(t, complete)
	require.Equal(t, map[int64]int{101: 1}, group20, "shared accounts must only be attributed to the actual request group")

	now := float64(time.Now().Unix())
	require.NoError(t, client.ZAdd(ctx, groupSlotKey(10), redis.Z{Score: now, Member: "legacy-request-id"}).Err())
	partial, complete, _, err := cache.GetGroupAccountConcurrency(ctx, 10)
	require.NoError(t, err)
	require.False(t, complete)
	require.Equal(t, map[int64]int{101: 2, 202: 1}, partial)
}

func TestGroupSlotReleaseUsesAccountScopedMember(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	ctx := context.Background()

	require.NoError(t, cache.TrackGroupSlot(ctx, 10, 101, "same-request"))
	require.NoError(t, cache.TrackGroupSlot(ctx, 10, 202, "same-request"))
	require.NoError(t, cache.ReleaseGroupSlot(ctx, 10, 101, "same-request"))

	counts, complete, _, err := cache.GetGroupAccountConcurrency(ctx, 10)
	require.NoError(t, err)
	require.True(t, complete)
	require.Equal(t, map[int64]int{202: 1}, counts)
}
