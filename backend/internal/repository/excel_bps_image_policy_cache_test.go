//go:build unit

package repository

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExcelBPSImagePolicyRedisAtomicAndExpiry(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	c := &gatewayCache{rdb: client}
	ctx := context.Background()
	var first atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, e := c.ClaimBPSImageWarning(ctx, "scope")
			if e != nil {
				t.Error(e)
			}
			if ok {
				first.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, first.Load())
	require.NoError(t, c.SaveBPSImageProgress(ctx, "scope", "", "a"))
	require.Error(t, c.SaveBPSImageProgress(ctx, "scope", "", "stale"))
	v, e := c.LoadBPSImageProgress(ctx, "scope")
	require.NoError(t, e)
	require.Equal(t, "a", v)
	ok, e := c.ClaimBPSImageWarning(ctx, "other")
	require.NoError(t, e)
	require.True(t, ok)
	server.FastForward(2*time.Hour + time.Second)
	v, e = c.LoadBPSImageProgress(ctx, "scope")
	require.NoError(t, e)
	require.Empty(t, v)
	ok, e = c.ClaimBPSImageWarning(ctx, "scope")
	require.NoError(t, e)
	require.True(t, ok)
	require.NoError(t, c.ResetBPSImageWarning(ctx, "scope"))
	ok, e = c.ClaimBPSImageWarning(ctx, "scope")
	require.NoError(t, e)
	require.True(t, ok)
	server.Close()
	_, e = c.LoadBPSImageProgress(ctx, "scope")
	require.Error(t, e)
}
