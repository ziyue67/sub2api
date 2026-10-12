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

// 本文件把"归还/续期"的既有契约下沉到**本地可跑**的单测（miniredis）。
//
// 为什么需要：这些契约原本只有 integration 套件覆盖（需要 Docker），于是给预留加了
// DB 兜底分支时，重构把"Redis 报告凭据已随 TTL 回收 ⇒ 返回 ErrBillingReservationExpired"
// 这条回退漏掉了（兜底不适用时直接返回 nil），4 个既有集成用例在 CI 才炸出来。
// 有这层烟测，同类回归本机能当场发现。

func newReservationContractCache(t *testing.T) (*billingCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	// 刻意用**不带 DB 兜底**的构造：这是"降级装配/测试"的形态，也是最容易被重构改坏的那个分支。
	cache, ok := NewBillingCache(rdb).(*billingCache)
	require.True(t, ok)
	require.Nil(t, cache.db, "本文件覆盖的是无 DB 兜底的形态")
	return cache, mr
}

func TestReservationReleaseContractWithoutDBFallback(t *testing.T) {
	cache, _ := newReservationContractCache(t)
	ctx := context.Background()
	const scope = "7301"

	// 1) 正常归还：凭据存在 ⇒ 成功，且额度归零、凭据被删除。
	total, err := cache.ReserveUserBalance(ctx, scope, "req-a", 0.30, 10*time.Minute)
	require.NoError(t, err)
	require.InDelta(t, 0.30, total, 1e-9)

	require.NoError(t, cache.ReleaseUserBalanceReservation(ctx, scope, "req-a", 0.30, 10*time.Minute))
	peeked, err := cache.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(t, err)
	require.InDelta(t, 0, peeked, 1e-9, "归还后聚合总额应归零")

	// 2) 再次归还同一笔：凭据已被删除 ⇒ 必须返回 ErrBillingReservationExpired，而不是 nil。
	//    这是关键语义：调用方据此把它计为"TTL 自愈已回收"，而不是"归还失败"。
	require.ErrorIs(t,
		cache.ReleaseUserBalanceReservation(ctx, scope, "req-a", 0.30, 10*time.Minute),
		service.ErrBillingReservationExpired,
		"凭据缺失的归还必须报告 ErrBillingReservationExpired")

	// 3) 从未存在过的凭据：同样报"已回收"。
	require.ErrorIs(t,
		cache.ReleaseUserBalanceReservation(ctx, scope, "never-existed", 0.10, 10*time.Minute),
		service.ErrBillingReservationExpired)
}

func TestReservationRenewContractWithoutDBFallback(t *testing.T) {
	cache, _ := newReservationContractCache(t)
	ctx := context.Background()
	const scope = "7302"

	_, err := cache.ReserveUserBalance(ctx, scope, "req-a", 0.20, 10*time.Minute)
	require.NoError(t, err)

	// 1) 凭据仍在 ⇒ 续期成功。
	require.NoError(t, cache.RenewUserBalanceReservation(ctx, scope, "req-a", 10*time.Minute))

	// 2) 归还之后续期 ⇒ 必须报告"已回收"（调用方据此停止心跳）。
	require.NoError(t, cache.ReleaseUserBalanceReservation(ctx, scope, "req-a", 0.20, 10*time.Minute))
	require.ErrorIs(t,
		cache.RenewUserBalanceReservation(ctx, scope, "req-a", 10*time.Minute),
		service.ErrBillingReservationExpired,
		"凭据缺失的续期必须报告 ErrBillingReservationExpired")

	// 3) 从未存在过的凭据：同样报"已回收"。
	require.ErrorIs(t,
		cache.RenewUserBalanceReservation(ctx, scope, "never-existed", 10*time.Minute),
		service.ErrBillingReservationExpired)
}

// TestReservationReleaseDoesNotEatOthersWithoutDBFallback 锁死"晚到的归还不得吃别人的预留"。
func TestReservationReleaseDoesNotEatOthersWithoutDBFallback(t *testing.T) {
	cache, _ := newReservationContractCache(t)
	ctx := context.Background()
	const scope = "7303"

	_, err := cache.ReserveUserBalance(ctx, scope, "req-a", 0.10, 10*time.Minute)
	require.NoError(t, err)
	_, err = cache.ReserveUserBalance(ctx, scope, "req-b", 0.20, 10*time.Minute)
	require.NoError(t, err)

	// req-a 的凭据"已过期"（先归还掉，模拟 TTL 回收后晚到的归还）。
	require.NoError(t, cache.ReleaseUserBalanceReservation(ctx, scope, "req-a", 0.10, 10*time.Minute))
	require.ErrorIs(t,
		cache.ReleaseUserBalanceReservation(ctx, scope, "req-a", 0.10, 10*time.Minute),
		service.ErrBillingReservationExpired)

	// req-b 的预留必须完好无损。
	peeked, err := cache.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(t, err)
	require.InDelta(t, 0.20, peeked, 1e-9, "晚到的归还不得递减他人预留")
}
