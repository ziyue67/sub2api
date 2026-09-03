package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newLaneTestCache(t *testing.T) (*concurrencyCache, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache, ok := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	require.True(t, ok)
	return cache, client
}

func TestLaneConcurrencyCache_AcquireReleaseAndIsolation(t *testing.T) {
	cache, rdb := newLaneTestCache(t)
	ctx := context.Background()

	// A lane has its own limit and does not consume the legacy account bucket.
	ok, err := cache.AcquireLaneSlot(ctx, 101, 2, "lane-req-1")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.AcquireLaneSlot(ctx, 101, 2, "lane-req-2")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.AcquireLaneSlot(ctx, 101, 2, "lane-req-3")
	require.NoError(t, err)
	require.False(t, ok)

	count, err := cache.GetLaneConcurrency(ctx, 101)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	accountOK, err := cache.AcquireAccountSlot(ctx, 55, 1, "account-req")
	require.NoError(t, err)
	require.True(t, accountOK, "lane slots must not consume account slots")

	require.NoError(t, cache.ReleaseLaneSlot(ctx, 101, "lane-req-1"))
	ok, err = cache.AcquireLaneSlot(ctx, 101, 2, "lane-req-3")
	require.NoError(t, err)
	require.True(t, ok, "released lane capacity should be reusable")

	count, err = cache.GetLaneConcurrency(ctx, 101)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	_, err = rdb.ZScore(ctx, laneSlotKey(101), "lane-req-1").Result()
	require.ErrorIs(t, err, redis.Nil)
}

func TestLaneConcurrencyCache_SameRequestRefreshesWithoutDoubleCounting(t *testing.T) {
	cache, _ := newLaneTestCache(t)
	ctx := context.Background()

	ok, err := cache.AcquireLaneSlot(ctx, 102, 1, "retryable")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.AcquireLaneSlot(ctx, 102, 1, "retryable")
	require.NoError(t, err)
	require.True(t, ok)
	count, err := cache.GetLaneConcurrency(ctx, 102)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestLaneConcurrencyCache_WaitQueueAndBatch(t *testing.T) {
	cache, _ := newLaneTestCache(t)
	ctx := context.Background()

	ok, err := cache.IncrementLaneWaitCount(ctx, 201, 2)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.IncrementLaneWaitCount(ctx, 201, 2)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.IncrementLaneWaitCount(ctx, 201, 2)
	require.NoError(t, err)
	require.False(t, ok)

	waiting, err := cache.GetLaneWaitingCount(ctx, 201)
	require.NoError(t, err)
	require.Equal(t, 2, waiting)
	require.NoError(t, cache.DecrementLaneWaitCount(ctx, 201))
	waiting, err = cache.GetLaneWaitingCount(ctx, 201)
	require.NoError(t, err)
	require.Equal(t, 1, waiting)

	require.True(t, mustAcquireLane(t, cache, 202, 3, "batch-1"))
	require.True(t, mustAcquireLane(t, cache, 203, 3, "batch-2"))
	counts, err := cache.GetLaneConcurrencyBatch(ctx, []int64{202, 203, 204, 202})
	require.NoError(t, err)
	require.Equal(t, 1, counts[202])
	require.Equal(t, 1, counts[203])
	require.Equal(t, 0, counts[204])
}

func TestLaneConcurrencyCache_ExpiredSlotsAreReaped(t *testing.T) {
	cache, _ := newLaneTestCache(t)
	ctx := context.Background()
	// Use a short-lived cache instance to avoid a 15 minute test clock jump.
	cache.slotTTLSeconds = 2
	require.True(t, mustAcquireLane(t, cache, 301, 1, "expires"))
	// miniredis is owned by the client in the helper; advancing Redis time is
	// not available through the client, so manually backdate the member score.
	now, err := cache.redisUnixSeconds(ctx)
	require.NoError(t, err)
	require.NoError(t, cache.rdb.ZAdd(ctx, laneSlotKey(301), redis.Z{Score: float64(now - 3), Member: "expires"}).Err())
	count, err := cache.GetLaneConcurrency(ctx, 301)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func mustAcquireLane(t *testing.T, cache *concurrencyCache, laneID int64, max int, requestID string) bool {
	t.Helper()
	ok, err := cache.AcquireLaneSlot(context.Background(), laneID, max, requestID)
	require.NoError(t, err)
	return ok
}

func TestLaneConcurrencyCache_ImplementsOptionalServiceContracts(t *testing.T) {
	cache, _ := newLaneTestCache(t)
	var _ service.LaneConcurrencyCache = cache
	var _ service.LaneConcurrencyBatchCache = cache
	// Keep the test explicit so a future refactor cannot accidentally return a
	// cache that only implements the legacy account interface.
	require.NotNil(t, cache)
}
