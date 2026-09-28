package repository

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestStrictRPMAcquireConcurrentAndReset(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Unix(1_800_000_010, 0)
	server.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &RPMCacheImpl{rdb: client}
	ctx := context.Background()
	const limit = 15
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			allowed, count, reset, err := cache.TryAcquireRPM(ctx, 42, limit)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			if count > limit {
				t.Errorf("count %d exceeds %d", count, limit)
			}
			if !reset.Equal(now.Truncate(time.Minute).Add(time.Minute)) {
				t.Errorf("unexpected reset: %s", reset)
			}
			if allowed {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, limit, accepted.Load())
	count, err := cache.GetRPM(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, limit, count)
	server.SetTime(now.Add(time.Minute))
	allowed, count, _, err := cache.TryAcquireRPM(ctx, 42, limit)
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, count)
}

func TestStrictRPMRefusesStaleMinuteBucket(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Unix(1_800_000_010, 0)
	server.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	oldMinute := now.Unix()/60 - 1
	key := fmt.Sprintf("%s42:%d", rpmKeyPrefix, oldMinute)
	result, err := tryRPMScript.Run(context.Background(), client, []string{key}, 15, 120, oldMinute).Slice()
	require.NoError(t, err)
	require.Equal(t, []any{int64(-1), int64(0), now.Unix() / 60}, result)
	require.False(t, server.Exists(key), "a delayed acquire must not charge the previous minute")
}
