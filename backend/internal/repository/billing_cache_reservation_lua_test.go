package repository

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 在真实 Redis 上验证预留 Lua（设置 BILLING_RESERVATION_REDIS_ADDR 时运行）：
// 过期成员不计入聚合、被拒请求不留痕、归还只动本笔、并发准入不超上限。
func newReservationLuaTestCache(t *testing.T) (*billingCache, string) {
	t.Helper()
	addr := os.Getenv("BILLING_RESERVATION_REDIS_ADDR")
	if addr == "" {
		t.Skip("BILLING_RESERVATION_REDIS_ADDR not set")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	require.NoError(t, rdb.Ping(context.Background()).Err())
	scope := fmt.Sprintf("luatest%d", time.Now().UnixNano())
	return &billingCache{rdb: rdb}, scope
}

func TestReservationLua_ExpiredMembersDropOutOfTotal(t *testing.T) {
	c, scope := newReservationLuaTestCache(t)
	ctx := context.Background()

	// 一笔「泄漏」的预留（结算丢失，从未归还），TTL 很短。
	_, ok, err := c.TryReserveUserBalance(ctx, scope, "leak", 5, 10, 150*time.Millisecond)
	require.NoError(t, err)
	require.True(t, ok)

	// 持续流量：每笔被接受的预留都会刷新索引键 TTL，但泄漏成员本身仍按自己的时刻过期。
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("live%d", i)
		_, ok, err = c.TryReserveUserBalance(ctx, scope, id, 1, 10, time.Minute)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, c.ReleaseUserBalanceReservation(ctx, scope, id, 1, time.Minute))
		time.Sleep(60 * time.Millisecond)
	}

	total, err := c.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(t, err)
	require.InDelta(t, 0, total, 1e-9, "leaked reservation must expire even under continuous traffic")

	// 过期后到达的归还不得动别人的预留。
	_, ok, err = c.TryReserveUserBalance(ctx, scope, "other", 3, 10, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	require.ErrorIs(t, c.ReleaseUserBalanceReservation(ctx, scope, "leak", 5, time.Minute), service.ErrBillingReservationExpired)
	total, err = c.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(t, err)
	require.InDelta(t, 3, total, 1e-9)

	// 已过期成员不能被续期复活。
	require.ErrorIs(t, c.RenewUserBalanceReservation(ctx, scope, "leak", time.Minute), service.ErrBillingReservationExpired)
	require.NoError(t, c.RenewUserBalanceReservation(ctx, scope, "other", time.Minute))
}

func TestReservationLua_RejectionLeavesNoTraceAndIsIdempotent(t *testing.T) {
	c, scope := newReservationLuaTestCache(t)
	ctx := context.Background()

	_, ok, err := c.TryReserveUserBalance(ctx, scope, "a", 0.1, 0.3, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	total, ok, err := c.TryReserveUserBalance(ctx, scope, "b", 0.2, 0.3, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	require.InDelta(t, 0.3, total, 1e-12)

	total, ok, err = c.TryReserveUserBalance(ctx, scope, "c", 0.01, 0.3, time.Minute)
	require.NoError(t, err)
	require.False(t, ok)
	require.InDelta(t, 0.3, total, 1e-12)
	require.ErrorIs(t, c.ReleaseUserBalanceReservation(ctx, scope, "c", 0.01, time.Minute), service.ErrBillingReservationExpired,
		"rejected request must not leave a receipt")

	// 同一 requestID 重复预留只续期，不重复累加。
	_, ok, err = c.TryReserveUserBalance(ctx, scope, "a", 0.1, 0.3, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	total, err = c.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(t, err)
	require.InDelta(t, 0.3, total, 1e-12)

	require.NoError(t, c.ReleaseUserBalanceReservation(ctx, scope, "a", 0.1, time.Minute))
	require.NoError(t, c.ReleaseUserBalanceReservation(ctx, scope, "b", 0.2, time.Minute))
	require.ErrorIs(t, c.ReleaseUserBalanceReservation(ctx, scope, "b", 0.2, time.Minute), service.ErrBillingReservationExpired,
		"double release is a no-op")
	total, err = c.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(t, err)
	require.Zero(t, total)
	n, err := c.rdb.Exists(ctx, billingReservedKey(scope), billingReservedAmountKey(scope)).Result()
	require.NoError(t, err)
	require.Zero(t, n, "empty ledger keys are removed")

	// 导出给 DB 兜底的凭据键能被还原出 scope / requestID。
	_, ok, err = c.TryReserveUserBalance(ctx, scope, "z", 1, 10, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	gotScope, gotID, parsed := splitReservationItemKey(billingReservedItemKey(scope, "z"))
	require.True(t, parsed)
	require.Equal(t, scope, gotScope)
	require.Equal(t, "z", gotID)
	require.NoError(t, c.ReleaseUserBalanceReservation(ctx, scope, "z", 1, time.Minute))
}

func TestReservationLua_ConcurrentAdmissionNeverExceedsLimit(t *testing.T) {
	c, scope := newReservationLuaTestCache(t)
	ctx := context.Background()
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, ok, err := c.TryReserveUserBalance(ctx, scope, fmt.Sprintf("r%d", i), 0.25, 10, time.Minute)
			if err == nil && ok {
				accepted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, int64(40), accepted.Load())
	total, err := c.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(t, err)
	require.InDelta(t, 10, total, 1e-9)
}

// 审计 H1：客户端断开（请求 ctx 已取消）不是 Redis 故障，不得切换到 DB 账本；
// Redis 健康时的"凭据已过期"同样不得切换。sqlmock 未登记任何期望，一旦触碰 DB
// （探测 / 导入 / 抬升窗口）就会让 ExpectationsWereMet 失败。
func TestReservationFallback_NotActivatedByCanceledContextOrExpiredReceipt(t *testing.T) {
	addr := os.Getenv("BILLING_RESERVATION_REDIS_ADDR")
	if addr == "" {
		t.Skip("BILLING_RESERVATION_REDIS_ADDR not set")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	c := &billingCache{rdb: rdb, db: db}
	// 预置探测缓存为"窗口未生效"，避免第一次探测本身查询 DB。
	c.fallbackProbeAt = time.Now().Add(time.Hour)
	scope := fmt.Sprintf("h1test%d", time.Now().UnixNano())

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = c.TryReserveUserBalance(canceled, scope, "r1", 1, 10, time.Minute)
	require.ErrorIs(t, err, context.Canceled)
	_, err = c.ReserveUserBalance(canceled, scope, "r1", 1, time.Minute)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, mock.ExpectationsWereMet(), "canceled request must not touch the DB fallback")

	// Redis 健康、凭据不存在：只做 DB 幂等删除，不探测、不导入、不抬升窗口。
	mock.ExpectExec(`DELETE FROM billing_scope_reservations`).WillReturnResult(sqlmock.NewResult(0, 0))
	err = c.ReleaseUserBalanceReservation(context.Background(), scope, "missing", 1, time.Minute)
	require.ErrorIs(t, err, service.ErrBillingReservationExpired)
	require.NoError(t, mock.ExpectationsWereMet())
	c.fallbackProbeMu.Lock()
	require.False(t, c.fallbackObservedActive, "healthy redis must not activate the DB window")
	c.fallbackProbeMu.Unlock()
}

// 滚动发布兼容：新旧实例的钱包预留互相可见，且不重复计数。
func TestReservationLua_LegacyWalletReservationsAreSharedDuringRollout(t *testing.T) {
	c, _ := newReservationLuaTestCache(t)
	ctx := context.Background()
	userID := time.Now().UnixNano() % 1_000_000_000
	scope := fmt.Sprintf("%d", userID)
	t.Cleanup(func() {
		z, h := billingInflightKeys(userID)
		_ = c.rdb.Del(ctx, z, h).Err()
	})

	// 旧实例登记 $3。
	ok, _, err := c.ReserveInflightBalance(ctx, userID, "old-1", 3, 100, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)

	// 新实例看到旧预留：上限 4 时再放 $2 必须被拒。
	total, accepted, err := c.TryReserveUserBalance(ctx, scope, "new-1", 2, 4, time.Minute)
	require.NoError(t, err)
	require.False(t, accepted)
	require.InDelta(t, 3, total, 1e-9)
	_, accepted, err = c.TryReserveUserBalance(ctx, scope, "new-1", 1, 4, time.Minute)
	require.NoError(t, err)
	require.True(t, accepted)

	// 旧实例看到新预留（镜像）：余额 5、已有 3+1，再放 $2 必须被拒。
	ok, sum, err := c.ReserveInflightBalance(ctx, userID, "old-2", 2, 5, time.Minute)
	require.NoError(t, err)
	require.False(t, ok)
	require.InDelta(t, 4, sum, 1e-9)

	// 新侧求和不重复计入自己的镜像。
	got, err := c.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(t, err)
	require.InDelta(t, 4, got, 1e-9)

	// 归还同时撤掉镜像。
	require.NoError(t, c.ReleaseUserBalanceReservation(ctx, scope, "new-1", 1, time.Minute))
	require.NoError(t, c.ReleaseInflightBalance(ctx, userID, "old-1"))
	got, err = c.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(t, err)
	require.Zero(t, got)
	_, h := billingInflightKeys(userID)
	n, err := c.rdb.HLen(ctx, h).Result()
	require.NoError(t, err)
	require.Zero(t, n)
}
