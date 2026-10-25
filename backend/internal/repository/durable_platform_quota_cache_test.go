//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDurablePlatformQuotaCacheMissSchemaAndLegacyDirtyIsolation(t *testing.T) {
	cache, _ := newMiniRedisCache(t)
	ctx := context.Background()
	_, ok, err := cache.GetDurablePlatformQuotaCache(ctx, 1, "openai")
	require.NoError(t, err)
	require.False(t, ok)
	// A legacy schema miss is distinguishable from an applied increment.
	require.NoError(t, cache.SetUserPlatformQuotaCache(ctx, 1, "openai", &service.UserPlatformQuotaCacheEntry{}, time.Minute))
	require.ErrorIs(t, cache.IncrUserPlatformQuotaUsageCache(ctx, 1, "openai", 1, time.Minute, true), service.ErrUserPlatformQuotaCacheNotUpdated)
	legacy := &service.UserPlatformQuotaCacheEntry{SchemaVersion: service.UserPlatformQuotaCacheSchemaV1, DailyUsageUSD: 2}
	require.NoError(t, cache.SetUserPlatformQuotaCache(ctx, 1, "openai", legacy, time.Minute))
	require.NoError(t, cache.IncrUserPlatformQuotaUsageCache(ctx, 1, "openai", 0.5, time.Minute, true))
	combined := &service.UserPlatformQuotaCacheEntry{SchemaVersion: service.UserPlatformQuotaCacheSchemaV1, DailyUsageUSD: 3.5}
	require.NoError(t, cache.SetDurablePlatformQuotaCache(ctx, 1, "openai", combined, time.Minute))
	// Retrying the new event can invalidate repeatedly without erasing old
	// unflushed increments, or placing a combined total into the legacy dirty set.
	for i := 0; i < 2; i++ {
		require.NoError(t, cache.DeleteDurablePlatformQuotaCache(ctx, 1, "openai"))
	}
	keys, err := cache.PopDirtyUserPlatformQuotaKeys(ctx, 10)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	entries, err := cache.BatchGetUserPlatformQuotaCache(ctx, keys)
	require.NoError(t, err)
	require.Equal(t, 2.5, entries[0].DailyUsageUSD, "legacy flusher must never receive the combined 3.5 total")
}
