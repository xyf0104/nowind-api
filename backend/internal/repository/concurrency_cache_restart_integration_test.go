//go:build integration

package repository

import (
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func (s *ConcurrencyCacheSuite) TestCleanupStaleProcessSlots_RollingRestartRetainsLivePeers() {
	const id int64 = 4901
	peer := NewConcurrencyCache(s.rdb, testSlotTTLMinutes, 120).(*concurrencyCache)
	restarted := NewConcurrencyCache(s.rdb, testSlotTTLMinutes, 120).(*concurrencyCache)
	for _, requestID := range []string{"process-a-1", "process-b-1"} {
		ok, err := peer.AcquireAccountSlot(s.ctx, id, 2, requestID)
		require.NoError(s.T(), err)
		require.True(s.T(), ok)
		ok, err = peer.AcquireUserSlot(s.ctx, id, 2, requestID)
		require.NoError(s.T(), err)
		require.True(s.T(), ok)
		ok, err = peer.IncrementAccountWaitCount(s.ctx, id, 2)
		require.NoError(s.T(), err)
		require.True(s.T(), ok)
		ok, err = peer.IncrementWaitCount(s.ctx, id, 2)
		require.NoError(s.T(), err)
		require.True(s.T(), ok)
	}
	now, err := s.rawCache.redisUnixSeconds(s.ctx)
	require.NoError(s.T(), err)
	for _, spec := range []slotIndexSpec{accountSlotIndex, userSlotIndex} {
		// Matching prefixes expire too: age, not process identity, controls removal.
		require.NoError(s.T(), s.rdb.ZAdd(s.ctx, spec.slotKey(id),
			redis.Z{Score: float64(now - int64(testSlotTTL.Seconds())), Member: "restarted-1"},
			redis.Z{Score: float64(now - int64(testSlotTTL.Seconds()) - 1), Member: "dead-peer-1"},
		).Err())
		require.NoError(s.T(), s.rdb.Expire(s.ctx, spec.slotKey(id), 2*time.Minute).Err())
		// A stale index is not evidence that its slots or waiters are dead.
		require.NoError(s.T(), s.rdb.ZAdd(s.ctx, spec.indexKey, redis.Z{
			Score: float64(now - 1), Member: strconv.FormatInt(id, 10),
		}).Err())
	}
	for _, prefix := range []string{"restarted-", "another-restart-"} {
		require.NoError(s.T(), restarted.CleanupStaleProcessSlots(s.ctx, prefix))
		for _, spec := range []slotIndexSpec{accountSlotIndex, userSlotIndex} {
			members, err := s.rdb.ZRange(s.ctx, spec.slotKey(id), 0, -1).Result()
			require.NoError(s.T(), err)
			require.ElementsMatch(s.T(), []string{"process-a-1", "process-b-1"}, members)
			score, err := s.rdb.ZScore(s.ctx, spec.indexKey, strconv.FormatInt(id, 10)).Result()
			require.NoError(s.T(), err)
			require.Greater(s.T(), int64(score), now)
			waiting, err := s.rdb.Get(s.ctx, spec.waitKey(id)).Int()
			require.NoError(s.T(), err)
			require.Equal(s.T(), 2, waiting)
			for _, key := range []string{spec.slotKey(id), spec.waitKey(id)} {
				ttl, err := s.rdb.PTTL(s.ctx, key).Result()
				require.NoError(s.T(), err)
				s.AssertTTLWithin(ttl, time.Minute, 2*time.Minute)
			}
		}
	}
	ok, err := restarted.AcquireAccountSlot(s.ctx, id, 2, "new-request")
	require.NoError(s.T(), err)
	require.False(s.T(), ok, "restart must not free a live peer's capacity")
	ok, err = restarted.AcquireUserSlot(s.ctx, id, 2, "new-request")
	require.NoError(s.T(), err)
	require.False(s.T(), ok)
	ok, err = restarted.IncrementAccountWaitCount(s.ctx, id, 2)
	require.NoError(s.T(), err)
	require.False(s.T(), ok, "restart must not clear a peer's full wait queue")
	ok, err = restarted.IncrementWaitCount(s.ctx, id, 2)
	require.NoError(s.T(), err)
	require.False(s.T(), ok)

	require.NoError(s.T(), peer.ReleaseAccountSlot(s.ctx, id, "process-a-1"))
	require.NoError(s.T(), peer.ReleaseUserSlot(s.ctx, id, "process-a-1"))
	require.NoError(s.T(), peer.DecrementAccountWaitCount(s.ctx, id))
	require.NoError(s.T(), peer.DecrementWaitCount(s.ctx, id))
	ok, err = restarted.AcquireAccountSlot(s.ctx, id, 2, "new-request")
	require.NoError(s.T(), err)
	require.True(s.T(), ok, "normal release must still free capacity immediately")
	ok, err = restarted.AcquireUserSlot(s.ctx, id, 2, "new-request")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)
	for _, key := range []string{accountWaitKey(id), waitQueueKey(id)} {
		waiting, err := s.rdb.Get(s.ctx, key).Int()
		require.NoError(s.T(), err)
		require.Equal(s.T(), 1, waiting, "normal completion must only decrement its own count")
	}
}
