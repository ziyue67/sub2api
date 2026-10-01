package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestStartupCleanupPreservesPeerAdmissionAndWaits(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	peer := NewConcurrencyCache(client, 15, 60)
	joining := NewConcurrencyCache(client, 15, 60)

	acquired, err := peer.AcquireAccountSlot(ctx, 10, 1, "peer-account")
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = peer.AcquireUserSlot(ctx, 20, 1, "peer-user")
	require.NoError(t, err)
	require.True(t, acquired)
	for _, key := range []string{accountWaitKey(10), userSlotIndex.waitKey(20), accountWaitKey(999)} {
		require.NoError(t, client.Set(ctx, key, 2, time.Minute).Err())
	}

	require.NoError(t, joining.CleanupStaleProcessSlots(ctx, "joining-pod-"))
	acquired, err = joining.AcquireAccountSlot(ctx, 10, 1, "joining-account")
	require.NoError(t, err)
	require.False(t, acquired, "joining Pod must not erase a live peer's account slot")
	acquired, err = joining.AcquireUserSlot(ctx, 20, 1, "joining-user")
	require.NoError(t, err)
	require.False(t, acquired, "joining Pod must not erase a live peer's user slot")
	for _, key := range []string{accountWaitKey(10), userSlotIndex.waitKey(20), accountWaitKey(999)} {
		require.Equal(t, "2", client.Get(ctx, key).Val(), "startup must preserve peer wait counters")
	}
}

func TestStartupCleanupReapsExpiredSlotsPreservingFreshPeer(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	cache := NewConcurrencyCache(client, 15, 60)
	now, err := client.Time(ctx).Result()
	require.NoError(t, err)
	for _, spec := range []slotIndexSpec{accountSlotIndex, userSlotIndex} {
		require.NoError(t, client.ZAdd(ctx, spec.slotKey(10),
			redis.Z{Score: float64(now.Unix() - 901), Member: "crashed-pod-1"},
			redis.Z{Score: float64(now.Unix()), Member: "live-pod-1"},
		).Err())
		require.NoError(t, client.ZAdd(ctx, spec.indexKey, redis.Z{Score: float64(now.Unix() - 1), Member: "10"}).Err())
		require.NoError(t, client.Set(ctx, spec.waitKey(10), 1, time.Minute).Err())
	}
	require.NoError(t, cache.CleanupStaleProcessSlots(ctx, "joining-pod-"))
	for _, spec := range []slotIndexSpec{accountSlotIndex, userSlotIndex} {
		require.Equal(t, []string{"live-pod-1"}, client.ZRange(ctx, spec.slotKey(10), 0, -1).Val())
		require.Equal(t, "1", client.Get(ctx, spec.waitKey(10)).Val())
		require.Greater(t, client.ZScore(ctx, spec.indexKey, "10").Val(), float64(now.Unix()))
	}
}
