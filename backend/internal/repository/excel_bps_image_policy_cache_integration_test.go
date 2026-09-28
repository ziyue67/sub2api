//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExcelBPSImagePolicyRealRedis(t *testing.T) {
	ctx := context.Background()
	cache := &gatewayCache{rdb: integrationRedis}
	scope := fmt.Sprintf("image-policy-integration:%d", time.Now().UnixNano())
	t.Cleanup(func() {
		require.NoError(t, integrationRedis.Del(ctx,
			imagePolicyKey(scope, "progress"), imagePolicyKey(scope, "warning"),
			imagePolicyKey(scope+"-other", "warning")).Err())
	})

	require.NoError(t, cache.SaveBPSImageProgress(ctx, scope, "", "first"))
	require.Error(t, cache.SaveBPSImageProgress(ctx, scope, "", "stale"))
	require.NoError(t, cache.SaveBPSImageProgress(ctx, scope, "first", "next"))
	require.NoError(t, integrationRedis.Expire(ctx, imagePolicyKey(scope, "progress"), time.Minute).Err())
	value, err := cache.LoadBPSImageProgress(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, "next", value)
	ttl, err := integrationRedis.TTL(ctx, imagePolicyKey(scope, "progress")).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, 119*time.Minute)
	require.LessOrEqual(t, ttl, 2*time.Hour)

	var first atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := cache.ClaimBPSImageWarning(ctx, scope)
			if err != nil {
				t.Error(err)
				return
			}
			if claimed {
				first.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, first.Load())
	claimed, err := cache.ClaimBPSImageWarning(ctx, scope+"-other")
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, cache.ResetBPSImageWarning(ctx, scope))
	claimed, err = cache.ClaimBPSImageWarning(ctx, scope)
	require.NoError(t, err)
	require.True(t, claimed)
}
