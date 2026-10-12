//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// newAPIKeyCacheForLedger 构造一个挂着 miniredis 的 apiKeyCache（账本与计费缓存
// 在生产装配里共用同一条 Redis，这里只需要一条可用的客户端）。
func newAPIKeyCacheForLedger(t *testing.T) (*apiKeyCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &apiKeyCache{rdb: rdb}, mr
}

// 本文件锁定"快照冻结 ⇒ 额度重复放行"这一类超发缺口的三个核心不变式：
//  1. 订阅用量同步累加会递增变动代号，并使在途的旧快照失效；
//  2. 订阅缓存失效同样递增代号（删除后旧快照不得复活）；
//  3. 订阅用量累加在缓存键缺失时以 applied=false 报告（而不是静默成功）。
//
// 这三条与 API Key 额度账本（高水位只增不减）共同保证：预检不会长期依据
// 偏小的"已用额度/用量"放行。

func subscriptionSnapshot(status string, usage float64) *service.SubscriptionCacheData {
	return &service.SubscriptionCacheData{
		Status:       status,
		ExpiresAt:    time.Now().Add(time.Hour),
		DailyUsage:   usage,
		WeeklyUsage:  usage,
		MonthlyUsage: usage,
		Version:      time.Now().Unix(),
	}
}

func TestSubscriptionGeneration_UpdateUsageInvalidatesInFlightSnapshot(t *testing.T) {
	cache, _ := newReservationContractCache(t)
	ctx := context.Background()
	const (
		userID, groupID = int64(8101), int64(9101)
	)

	require.NoError(t, cache.SetSubscriptionCache(ctx, userID, groupID, subscriptionSnapshot("active", 5)))

	// 回源方在 DB 读取之前取代号……
	generation, err := cache.SubscriptionGeneration(ctx, userID, groupID)
	require.NoError(t, err)

	// ……读取期间发生了结算：同步累加用量（代号随之递增）。
	applied, err := cache.UpdateSubscriptionUsageApplied(ctx, userID, groupID, 3)
	require.NoError(t, err)
	require.True(t, applied, "缓存键存在时应真的累加")

	// 迟到的旧快照（usage=5）必须被拒绝，否则会把 8 覆盖回 5。
	published, err := cache.SetSubscriptionCacheIfGeneration(ctx, userID, groupID, subscriptionSnapshot("active", 5), generation)
	require.NoError(t, err)
	require.False(t, published, "读取期间发生过用量变动时不得发布旧快照")

	stored, err := cache.GetSubscriptionCache(ctx, userID, groupID)
	require.NoError(t, err)
	require.InDelta(t, 8, stored.DailyUsage, 1e-9, "缓存必须保留结算后的真实用量")
}

func TestSubscriptionGeneration_SnapshotPublishedWhenUnchanged(t *testing.T) {
	cache, _ := newReservationContractCache(t)
	ctx := context.Background()
	const (
		userID, groupID = int64(8102), int64(9102)
	)

	generation, err := cache.SubscriptionGeneration(ctx, userID, groupID)
	require.NoError(t, err)

	published, err := cache.SetSubscriptionCacheIfGeneration(ctx, userID, groupID, subscriptionSnapshot("active", 5), generation)
	require.NoError(t, err)
	require.True(t, published, "没有并发变动时必须正常发布（否则缓存永远建不起来）")

	stored, err := cache.GetSubscriptionCache(ctx, userID, groupID)
	require.NoError(t, err)
	require.InDelta(t, 5, stored.DailyUsage, 1e-9)
}

func TestSubscriptionGeneration_InvalidateBumpsGeneration(t *testing.T) {
	cache, _ := newReservationContractCache(t)
	ctx := context.Background()
	const (
		userID, groupID = int64(8103), int64(9103)
	)

	require.NoError(t, cache.SetSubscriptionCache(ctx, userID, groupID, subscriptionSnapshot("active", 5)))
	generation, err := cache.SubscriptionGeneration(ctx, userID, groupID)
	require.NoError(t, err)

	require.NoError(t, cache.InvalidateSubscriptionCache(ctx, userID, groupID))

	// 失效后落下的旧快照不得复活缓存（否则预检按陈旧用量放行）。
	published, err := cache.SetSubscriptionCacheIfGeneration(ctx, userID, groupID, subscriptionSnapshot("active", 5), generation)
	require.NoError(t, err)
	require.False(t, published, "缓存失效后旧快照不得复活")
}

func TestSubscriptionUsageApplied_ReportsSkippedIncrement(t *testing.T) {
	cache, _ := newReservationContractCache(t)
	ctx := context.Background()

	// 缓存键不存在：脚本静默跳过。调用方必须能区分"跳过"与"成功"，
	// 否则这笔用量只在 DB 里而预检看到的是偏小的缓存值。
	applied, err := cache.UpdateSubscriptionUsageApplied(ctx, 8104, 9104, 1)
	require.NoError(t, err)
	require.False(t, applied, "键缺失时累加被跳过，必须报告 applied=false")
}

func TestAPIKeyRateLimitUsageApplied_ReportsSkippedIncrement(t *testing.T) {
	cache, _ := newReservationContractCache(t)
	ctx := context.Background()

	applied, err := cache.UpdateAPIKeyRateLimitUsageApplied(ctx, 8105, 1)
	require.NoError(t, err)
	require.False(t, applied, "键缺失时累加被跳过，必须报告 applied=false")
}

func TestAPIKeyQuotaUsedLedger_MonotonicHighWater(t *testing.T) {
	// apiKeyCache 与 billingCache 共用同一条 Redis（生产装配里是同一客户端）；
	// 这里直接验证账本语义：只增不减，读缺失返回 0。
	apiCache, _ := newAPIKeyCacheForLedger(t)
	ctx := context.Background()

	used, err := apiCache.GetAPIKeyQuotaUsedLedger(ctx, 8106)
	require.NoError(t, err)
	require.Zero(t, used)

	require.NoError(t, apiCache.SetAPIKeyQuotaUsedLedger(ctx, 8106, 5))
	require.NoError(t, apiCache.SetAPIKeyQuotaUsedLedger(ctx, 8106, 3)) // 乱序小值不得覆盖

	used, err = apiCache.GetAPIKeyQuotaUsedLedger(ctx, 8106)
	require.NoError(t, err)
	require.InDelta(t, 5, used, 1e-9, "账本必须只增不减（写小 = 放行超额）")

	require.NoError(t, apiCache.SetAPIKeyQuotaUsedLedger(ctx, 8106, 9))
	used, err = apiCache.GetAPIKeyQuotaUsedLedger(ctx, 8106)
	require.NoError(t, err)
	require.InDelta(t, 9, used, 1e-9)

	// 管理员重置后必须能清除高水位，否则残留值会误拦该 key。
	require.NoError(t, apiCache.ClearAPIKeyQuotaUsedLedger(ctx, 8106))
	used, err = apiCache.GetAPIKeyQuotaUsedLedger(ctx, 8106)
	require.NoError(t, err)
	require.Zero(t, used)
}
