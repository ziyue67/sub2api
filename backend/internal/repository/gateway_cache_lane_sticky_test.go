package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newLaneStickyGatewayCacheTest(t *testing.T) (service.LaneStickyCache, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := NewGatewayCache(rdb)
	laneCache, ok := cache.(service.LaneStickyCache)
	require.True(t, ok, "gateway cache must expose optional lane sticky extension")
	return laneCache, rdb
}

func TestGatewayCacheLaneStickyCRUDAndTTL(t *testing.T) {
	laneCache, rdb := newLaneStickyGatewayCacheTest(t)
	ctx := context.Background()
	binding := service.LaneStickyBinding{AccountID: 29270, LaneID: 101}
	const groupID int64 = 7
	model := "gpt-5.6:luna"
	session := "session:abc"

	_, err := laneCache.GetSessionLane(ctx, groupID, model, session)
	require.ErrorIs(t, err, service.ErrLaneStickySessionNotFound)
	require.NoError(t, laneCache.SetSessionLane(ctx, groupID, model, session, binding, time.Minute))

	got, err := laneCache.GetSessionLane(ctx, groupID, model, session)
	require.NoError(t, err)
	require.Equal(t, binding, got)

	key := buildLaneStickySessionKey(groupID, model, session)
	require.Equal(t, "sub2api:sticky:lane:7:gpt-5.6%3Aluna:session%3Aabc", key)
	ttl, err := rdb.TTL(ctx, key).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, time.Duration(0))
	require.NoError(t, laneCache.RefreshSessionLaneTTL(ctx, groupID, model, session, 3*time.Minute))
	ttl, err = rdb.TTL(ctx, key).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, 2*time.Minute)

	// Deleting the lane key must not touch the legacy account sticky key.
	require.NoError(t, rdb.Set(ctx, buildSessionKey(groupID, session), "999", time.Minute).Err())
	require.NoError(t, laneCache.DeleteSessionLane(ctx, groupID, model, session))
	_, err = laneCache.GetSessionLane(ctx, groupID, model, session)
	require.ErrorIs(t, err, service.ErrLaneStickySessionNotFound)
	legacy, err := rdb.Get(ctx, buildSessionKey(groupID, session)).Int64()
	require.NoError(t, err)
	require.Equal(t, int64(999), legacy)
}

func TestGatewayCacheLaneStickyModelAndSessionIsolation(t *testing.T) {
	laneCache, _ := newLaneStickyGatewayCacheTest(t)
	ctx := context.Background()
	first := service.LaneStickyBinding{AccountID: 1, LaneID: 11}
	second := service.LaneStickyBinding{AccountID: 2, LaneID: 22}
	require.NoError(t, laneCache.SetSessionLane(ctx, 1, "model-a", "session", first, time.Minute))
	require.NoError(t, laneCache.SetSessionLane(ctx, 1, "model-b", "session", second, time.Minute))
	require.NoError(t, laneCache.SetSessionLane(ctx, 2, "model-a", "session", second, time.Minute))

	got, err := laneCache.GetSessionLane(ctx, 1, "model-a", "session")
	require.NoError(t, err)
	require.Equal(t, first, got)
	got, err = laneCache.GetSessionLane(ctx, 1, "model-b", "session")
	require.NoError(t, err)
	require.Equal(t, second, got)
	got, err = laneCache.GetSessionLane(ctx, 2, "model-a", "session")
	require.NoError(t, err)
	require.Equal(t, second, got)
}

func TestGatewayCacheLaneStickyRejectsInvalidPayload(t *testing.T) {
	laneCache, rdb := newLaneStickyGatewayCacheTest(t)
	ctx := context.Background()
	for _, binding := range []service.LaneStickyBinding{{}, {AccountID: 1}, {LaneID: 1}} {
		err := laneCache.SetSessionLane(ctx, 1, "model", "session", binding, time.Minute)
		require.Error(t, err)
	}
	require.Error(t, laneCache.SetSessionLane(ctx, 1, "", "session", service.LaneStickyBinding{AccountID: 1, LaneID: 1}, time.Minute))
	require.Error(t, laneCache.SetSessionLane(ctx, 1, "model", "", service.LaneStickyBinding{AccountID: 1, LaneID: 1}, time.Minute))
	require.Error(t, laneCache.SetSessionLane(ctx, 1, "model", "session", service.LaneStickyBinding{AccountID: 1, LaneID: 1}, 0))

	key := buildLaneStickySessionKey(1, "model", "bad")
	require.NoError(t, rdb.Set(ctx, key, `{"account_id":1}`, time.Minute).Err())
	_, err := laneCache.GetSessionLane(ctx, 1, "model", "bad")
	require.Error(t, err)
	require.False(t, errors.Is(err, service.ErrLaneStickySessionNotFound))
	// Refreshing a missing key remains a no-op, mirroring legacy sticky cache.
	require.NoError(t, laneCache.RefreshSessionLaneTTL(ctx, 1, "model", "missing", time.Minute))
}
