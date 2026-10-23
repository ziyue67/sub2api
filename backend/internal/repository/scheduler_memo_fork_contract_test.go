//go:build unit

package repository

import (
	"context"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerMemoForkNestedWritesStayRequestLocal(t *testing.T) {
	cache, _, _ := newSchedulerCacheMemoUnit(t, 16)
	bucket := memoTestBucket(970)
	account := memoTestAccount(971, "a")
	account.Credentials[service.DeepSeekResponsesImageFieldCredential] = "url"
	account.Extra["model_rate_limits"] = map[string]any{"scope": map[string]any{"reason": "original"}}
	account.AccountGroups = []service.AccountGroup{{AccountID: account.ID, GroupID: bucket.GroupID, AllowedModels: []string{"claude-sonnet"}}}
	memoPublish(t, cache, bucket, account)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidates, hit, err := cache.GetSnapshot(context.Background(), bucket)
			if err != nil || !hit || len(candidates) != 1 {
				t.Error("snapshot unavailable", err)
				return
			}
			own := candidates[0]
			own.Credentials["model_mapping"].(map[string]any)["claude-sonnet"] = "local"
			own.Extra["model_rate_limits"].(map[string]any)["scope"].(map[string]any)["reason"] = "local"
			own.AccountGroups[0].AllowedModels[0] = "local"
			own.GetModelMapping()["claude-sonnet"] = "local"
		}()
	}
	wg.Wait()
	actual := memoRead(t, cache, bucket)[0]
	require.Equal(t, "sonnet-a", actual.GetMappedModel("claude-sonnet"))
	require.Equal(t, "url", actual.GetCredential(service.DeepSeekResponsesImageFieldCredential))
	require.Equal(t, "original", actual.Extra["model_rate_limits"].(map[string]any)["scope"].(map[string]any)["reason"])
	require.Equal(t, []string{"claude-sonnet"}, actual.AccountGroups[0].AllowedModels)
}

func TestSchedulerMemoForkWarmControlAndUnversionedRevocation(t *testing.T) {
	for _, kind := range []string{"ready", "active", "metadata", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			cache, _, _ := newSchedulerCacheMemoUnit(t, 16)
			bucket := memoTestBucket(980)
			memoPublish(t, cache, bucket, memoTestAccount(981, "a"))
			memoRead(t, cache, bucket)
			ctx := context.Background()
			switch kind {
			case "ready":
				require.NoError(t, cache.rdb.Del(ctx, schedulerBucketKey(schedulerReadyPrefix, bucket)).Err())
			case "active":
				require.NoError(t, cache.rdb.Del(ctx, schedulerBucketKey(schedulerActivePrefix, bucket)).Err())
			case "metadata":
				require.NoError(t, cache.rdb.Del(ctx, schedulerAccountMetaKey("981")).Err())
			case "cancel":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			candidates, hit, err := cache.GetSnapshot(ctx, bucket)
			require.False(t, hit)
			require.Nil(t, candidates)
			if kind == "cancel" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
