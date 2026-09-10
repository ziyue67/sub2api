package repository

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// balanceExhaustionStore 与 service.balanceExhaustionStore 保持一致的结构约定。
// 这里用匿名接口断言，确保 repository.billingCache 一直提供该能力（否则预检的
// "钱包已耗尽"闸门会静默降级，线上又会回到"缓存被旧值复活 → 免费调用"的状态）。
type balanceExhaustionStoreForTest interface {
	MarkUserBalanceExhausted(ctx context.Context, userID int64) error
	ClearUserBalanceExhausted(ctx context.Context, userID int64) error
	IsUserBalanceExhausted(ctx context.Context, userID int64) (bool, error)
}

func TestBillingCache_BalanceExhaustedMarkerLifecycle(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cache := NewBillingCache(rdb)
	store, ok := cache.(balanceExhaustionStoreForTest)
	require.True(t, ok, "billingCache 必须实现钱包耗尽标记能力")

	ctx := context.Background()

	exhausted, err := store.IsUserBalanceExhausted(ctx, 7)
	require.NoError(t, err)
	require.False(t, exhausted)

	require.NoError(t, store.MarkUserBalanceExhausted(ctx, 7))
	exhausted, err = store.IsUserBalanceExhausted(ctx, 7)
	require.NoError(t, err)
	require.True(t, exhausted)
	require.True(t, mr.Exists(billingBalanceExhaustedKey(7)))

	// 关键不变式：标记与余额缓存是两个独立的 key。失效余额缓存（扣费后的常规动作）
	// 不能把标记一起清掉，否则"缓存被旧回源值复活"的窗口会重新打开。
	require.NoError(t, cache.InvalidateUserBalance(ctx, 7))
	exhausted, err = store.IsUserBalanceExhausted(ctx, 7)
	require.NoError(t, err)
	require.True(t, exhausted)

	require.NoError(t, store.ClearUserBalanceExhausted(ctx, 7))
	exhausted, err = store.IsUserBalanceExhausted(ctx, 7)
	require.NoError(t, err)
	require.False(t, exhausted)
}

func TestBillingCache_BalanceExhaustedMarkerOutlivesBalanceCacheTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cache := NewBillingCache(rdb)
	store, ok := cache.(balanceExhaustionStoreForTest)
	require.True(t, ok)

	ctx := context.Background()
	require.NoError(t, store.MarkUserBalanceExhausted(ctx, 9))

	// 标记的 TTL 必须 >= 余额缓存 TTL：否则标记先过期，而余额缓存里仍可能留着
	// 扣费前的偏旧余额，预检又会被放行。
	ttl := mr.TTL(billingBalanceExhaustedKey(9))
	require.Greater(t, ttl, billingCacheTTL)

	// 推进到余额缓存必然已过期、但标记仍有效的时刻：闸门必须仍然生效。
	mr.FastForward(billingCacheTTL + 1)
	exhausted, err := store.IsUserBalanceExhausted(ctx, 9)
	require.NoError(t, err)
	require.True(t, exhausted)
}
