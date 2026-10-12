//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// BillingReservationSuite 验证"在途预留"（并发准入护栏）在真实 Redis 上的原子语义：
// 累加与十进制往返、凭据驱动的精确归还、并发累加、TTL 自愈、心跳续期，
// 以及"凭据过期后的归还不得吃掉他人预留"这条关键正确性约束。
type BillingReservationSuite struct {
	IntegrationRedisSuite
}

// reservationCache 构造真实 Redis 上的 billingCache 并暴露内部预留方法。
func (s *BillingReservationSuite) reservationCache() (*billingCache, *redis.Client) {
	s.T().Helper()
	rdb := testRedis(s.T())
	cache, ok := NewBillingCache(rdb).(*billingCache)
	require.True(s.T(), ok, "NewBillingCache 应返回 *billingCache")
	return cache, rdb
}

func (s *BillingReservationSuite) TestReserveAccumulatesAndReportsTotal() {
	cache, rdb := s.reservationCache()
	ctx := context.Background()
	scope := "9001"
	key := billingReservedKeyPrefix + scope

	total, err := cache.ReserveUserBalance(ctx, scope, "req-a", 0.10, 10*time.Minute)
	require.NoError(s.T(), err, "ReserveUserBalance")
	require.InDelta(s.T(), 0.10, total, 1e-9, "首笔预留应从 0 累加到 0.10")

	// 第二笔累加：0.10 + 0.20 == 0.30，且小数必须精确（十进制往返，不能被整数截断）。
	total, err = cache.ReserveUserBalance(ctx, scope, "req-b", 0.20, 10*time.Minute)
	require.NoError(s.T(), err, "ReserveUserBalance 第二笔")
	require.InDelta(s.T(), 0.30, total, 1e-9, "两笔预留应累加为 0.30")

	exists, err := rdb.Exists(ctx, key).Result()
	require.NoError(s.T(), err, "Exists")
	require.Equal(s.T(), int64(1), exists, "预留键应存在")

	// 每笔请求都留有自己的凭据键。
	// 注意：夹具的 prefixHook 对 EXISTS 只改写第一个 key 参数，因此这里必须逐 key
	// 单发 EXISTS，不能用一次多 key 的 Exists（否则统计会偏小）。
	require.Equal(s.T(), int64(1), s.itemExists(rdb, scope, "req-a"), "req-a 应留有凭据")
	require.Equal(s.T(), int64(1), s.itemExists(rdb, scope, "req-b"), "req-b 应留有凭据")

	ttl, err := rdb.TTL(ctx, key).Result()
	require.NoError(s.T(), err, "TTL")
	s.AssertTTLWithin(ttl, 9*time.Minute, 10*time.Minute)

	peeked, err := cache.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(s.T(), err, "ReservedUserBalanceTotal")
	require.InDelta(s.T(), 0.30, peeked, 1e-9, "只读总额应与累加值一致")
}

func (s *BillingReservationSuite) TestTryReserveIsAtomicAndIdempotent() {
	cache, rdb := s.reservationCache()
	ctx := context.Background()
	scope := "9015"

	const workers = 8
	start := make(chan struct{})
	type result struct {
		requestID string
		total     float64
		accepted  bool
		err       error
	}
	results := make(chan result, workers)
	for i := 0; i < workers; i++ {
		requestID := fmt.Sprintf("atomic-%d", i)
		go func() {
			<-start
			total, accepted, err := cache.TryReserveUserBalance(ctx, scope, requestID, 0.10, 0.30, 10*time.Minute)
			results <- result{requestID: requestID, total: total, accepted: accepted, err: err}
		}()
	}
	close(start)

	accepted := 0
	var acceptedRequestID string
	for i := 0; i < workers; i++ {
		got := <-results
		require.NoError(s.T(), got.err)
		if got.accepted {
			accepted++
			if acceptedRequestID == "" {
				acceptedRequestID = got.requestID
			}
		}
	}
	require.Equal(s.T(), 3, accepted, "原子上限只能接受 3 笔")
	require.NotEmpty(s.T(), acceptedRequestID, "至少应有一笔请求被接受")
	total, err := cache.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(s.T(), err)
	require.InDelta(s.T(), 0.30, total, 1e-9)

	// Replaying the same request must not add another 0.10.
	var replayAccepted bool
	total, replayAccepted, err = cache.TryReserveUserBalance(ctx, scope, acceptedRequestID, 0.10, 0.30, 10*time.Minute)
	require.NoError(s.T(), err)
	require.True(s.T(), replayAccepted)
	require.InDelta(s.T(), 0.30, total, 1e-9)
	require.Equal(s.T(), int64(1), rdb.Exists(ctx, billingReservedItemKey(scope, acceptedRequestID)).Val())
}

// TestTryReserveRebuildsAggregateWhenReceiptSurvives 锁死审计 R3。
//
// 凭据仍在、聚合键却消失（被 maxmemory 淘汰，或某笔极小金额的归还触发了
// `newVal <= 1e-7` 的 DEL 分支）时，旧脚本返回 '-1'，被 Go 侧当成"预留后端故障"
// → fail-open，本笔在**完全没有预留**的情况下被放行。修复后必须以凭据金额重建聚合
// 并接受本笔，恢复"凭据存在 ⇒ 聚合至少包含本笔"的不变量。
func (s *BillingReservationSuite) TestTryReserveRebuildsAggregateWhenReceiptSurvives() {
	cache, rdb := s.reservationCache()
	ctx := context.Background()
	scope := "9016"

	total, accepted, err := cache.TryReserveUserBalance(ctx, scope, "receipt", 0.25, 1.0, 10*time.Minute)
	require.NoError(s.T(), err)
	require.True(s.T(), accepted)
	require.InDelta(s.T(), 0.25, total, 1e-9)

	// 只删聚合键，保留凭据 —— 复现"凭据在、聚合不在"的状态不一致。
	require.NoError(s.T(), rdb.Del(ctx, billingReservedKey(scope)).Err())
	require.Equal(s.T(), int64(1), s.itemExists(rdb, scope, "receipt"), "凭据必须仍在")

	total, accepted, err = cache.TryReserveUserBalance(ctx, scope, "receipt", 0.25, 1.0, 10*time.Minute)
	require.NoError(s.T(), err, "凭据仍在时必须能重建聚合，不得返回错误（错误会被上层当成 fail-open）")
	require.True(s.T(), accepted, "重建后本笔应被接受")
	require.InDelta(s.T(), 0.25, total, 1e-9, "聚合应恰好等于凭据金额，不能重复累加")
}

// TestTryReserveKeepsFullFloatPrecision 锁死审计 R4。
//
// 新脚本用 Lua 手写累加替代 INCRBYFLOAT，而 `tostring()` 走 lua_number2str（%.14g），
// 会把 0.1 三笔累加的结果 0.30000000000000004 截断成 0.3，与 release/renew 侧
// INCRBYFLOAT 的 17 位口径不一致。修复后显式用 %.17g 写回，聚合值应与 Go 侧同样的
// IEEE754 累加结果**逐位相同**。
func (s *BillingReservationSuite) TestTryReserveKeepsFullFloatPrecision() {
	cache, _ := s.reservationCache()
	ctx := context.Background()
	scope := "9017"

	want := 0.0
	for i := 0; i < 3; i++ {
		want += 0.1 // 必须**运行时**累加：常量表达式 `0.1+0.1+0.1` 会被编译器按任意精度
		// 折叠成精确的 0.3（再舍入到最近的 float64），复现不出浮点漂移。
	}
	require.Equal(s.T(), 0.30000000000000004, want, "前提校验：运行时三笔累加确实带浮点尾差")
	for i := 0; i < 3; i++ {
		_, accepted, err := cache.TryReserveUserBalance(
			ctx, scope, fmt.Sprintf("precision-%d", i), 0.10, 1.0, 10*time.Minute)
		require.NoError(s.T(), err)
		require.True(s.T(), accepted)
	}

	got, err := cache.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(s.T(), err)
	require.Equal(s.T(), want, got, "聚合必须保留完整 float64 精度（%.17g），不得被 %.14g 截断")
}

// TestReserveDoesNotRenewAggregateTTLOnLaterReserves 是"聚合键 TTL 不得被反复续期"的
// 回归测试（对应真实的"账号在封底之上被永久锁死"故障）。
//
// 放行判定的顺序是「先落凭据+累加聚合 → 脚本返回后再在 Go 侧校验封底护栏 →
// 护栏不成立就 release 回滚本笔」。也就是说**被拒绝的请求也会完整走完预留脚本**，
// 回滚只归还金额、不归还 TTL。若脚本在每次预留时都无条件 PEXPIRE 聚合键，那么每一次
// 被拒绝的请求都会把历史泄漏（崩溃 / 丢结算 / 晚到归还被忽略而从未递减的金额）的
// 过期时间重新续满：并发突发下拒绝量远大于放行量，泄漏被反复续期、永不消失，
// 用户会在余额远高于封底时就被"在途预留"持续 403，且只要还有流量就永远无法自愈。
//
// 这里断言：后续预留不得把聚合键的剩余 TTL 顶回接近满值，必须继续自然衰减。
func (s *BillingReservationSuite) TestReserveDoesNotRenewAggregateTTLOnLaterReserves() {
	cache, rdb := s.reservationCache()
	ctx := context.Background()
	scope := "9010"
	key := billingReservedKeyPrefix + scope

	const ttl = 10 * time.Second
	_, err := cache.ReserveUserBalance(ctx, scope, "req-first", 0.10, ttl)
	require.NoError(s.T(), err, "首笔预留")

	// 让 TTL 明显衰减后再累加第二笔（模拟并发突发中被拒绝的那一批请求）。
	time.Sleep(3 * time.Second)
	_, err = cache.ReserveUserBalance(ctx, scope, "req-second", 0.10, ttl)
	require.NoError(s.T(), err, "第二笔累加")

	remaining, err := rdb.TTL(ctx, key).Result()
	require.NoError(s.T(), err, "TTL")
	require.Less(s.T(), remaining, 8*time.Second,
		"累加不得续期：剩余 TTL 应继续衰减；若被 PEXPIRE 顶回 ~10s，说明泄漏会被无限续期")

	// 被拒绝请求的真实路径：预留 → 回滚（release）。回滚同样不得把聚合键 TTL 续满。
	require.NoError(s.T(), cache.ReleaseUserBalanceReservation(ctx, scope, "req-second", 0.10, ttl), "回滚第二笔")
	remainingAfterRollback, err := rdb.TTL(ctx, key).Result()
	require.NoError(s.T(), err, "TTL after rollback")
	require.Less(s.T(), remainingAfterRollback, 8*time.Second,
		"回滚不得续期：被拒绝的请求不能给历史泄漏续命")
}

func (s *BillingReservationSuite) TestReserveIsIdempotentForSameRequestID() {
	cache, _ := s.reservationCache()
	ctx := context.Background()
	scope := "9002"

	_, err := cache.ReserveUserBalance(ctx, scope, "req-a", 0.10, 10*time.Minute)
	require.NoError(s.T(), err, "第一笔")

	// 同一 requestID 重复预留（重试 / 重复绑定）不得重复累加：否则同一笔请求会被记两次额度。
	total, err := cache.ReserveUserBalance(ctx, scope, "req-a", 0.10, 10*time.Minute)
	require.NoError(s.T(), err, "重复预留同一 requestID")
	require.InDelta(s.T(), 0.10, total, 1e-9, "同 requestID 重复预留必须幂等")
}

func (s *BillingReservationSuite) TestReserveFallsBackToCacheTTLWhenTTLMissing() {
	cache, rdb := s.reservationCache()
	ctx := context.Background()
	scope := "9003"
	key := billingReservedKeyPrefix + scope

	_, err := cache.ReserveUserBalance(ctx, scope, "req-a", 0.05, 0)
	require.NoError(s.T(), err, "ReserveUserBalance 应以缺省 TTL 兜底")

	ttl, err := rdb.TTL(ctx, key).Result()
	require.NoError(s.T(), err, "TTL")
	s.AssertTTLWithin(ttl, billingCacheTTL-30*time.Second, billingCacheTTL)
}

func (s *BillingReservationSuite) TestReserveRejectsInvalidArguments() {
	cache, _ := s.reservationCache()
	ctx := context.Background()

	_, err := cache.ReserveUserBalance(ctx, "9004", "req-a", -0.10, 10*time.Minute)
	require.Error(s.T(), err, "负数预留必须被拒绝（否则可凭空放大可花余额）")

	_, err = cache.ReserveUserBalance(ctx, "", "req-a", 0.10, 10*time.Minute)
	require.Error(s.T(), err, "空 scope 必须被拒绝")

	_, err = cache.ReserveUserBalance(ctx, "9004", "", 0.10, 10*time.Minute)
	require.Error(s.T(), err, "空 requestID 必须被拒绝（凭据无法定位就无法安全归还）")
}

func (s *BillingReservationSuite) TestReleaseReducesAndDeletesAtZero() {
	cache, rdb := s.reservationCache()
	ctx := context.Background()
	scope := "9005"
	key := billingReservedKeyPrefix + scope

	_, err := cache.ReserveUserBalance(ctx, scope, "req-a", 0.30, 10*time.Minute)
	require.NoError(s.T(), err, "ReserveUserBalance")

	require.NoError(s.T(), cache.ReleaseUserBalanceReservation(ctx, scope, "req-a", 0.10, 10*time.Minute), "归还本笔预留")

	rest, err := cache.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(s.T(), err, "读回总额")
	require.InDelta(s.T(), 0.0, rest, 1e-9, "归还后凭据消失、聚合键应被删除")

	exists, err := rdb.Exists(ctx, key).Result()
	require.NoError(s.T(), err, "Exists")
	require.Equal(s.T(), int64(0), exists, "归零后预留键必须被删除")

	// 凭据已删除后再归还：返回 ErrBillingReservationExpired（no-op），不得把总额减成负数。
	err = cache.ReleaseUserBalanceReservation(ctx, scope, "req-a", 0.10, 10*time.Minute)
	require.ErrorIs(s.T(), err, service.ErrBillingReservationExpired, "凭据缺失时的归还必须安全拒绝")
	exists, err = rdb.Exists(ctx, key).Result()
	require.NoError(s.T(), err, "Exists after no-op release")
	require.Equal(s.T(), int64(0), exists, "no-op 归还不得重新创建键")
}

func (s *BillingReservationSuite) TestReleaseOnMissingReceiptIsNoop() {
	cache, rdb := s.reservationCache()
	ctx := context.Background()
	scope := "9006"
	key := billingReservedKeyPrefix + scope

	err := cache.ReleaseUserBalanceReservation(ctx, scope, "never-reserved", 0.10, 10*time.Minute)
	require.ErrorIs(s.T(), err, service.ErrBillingReservationExpired, "从未预留过也要安全 no-op")

	exists, err := rdb.Exists(ctx, key).Result()
	require.NoError(s.T(), err, "Exists")
	require.Equal(s.T(), int64(0), exists, "no-op 归还不得创建键")
}

// TestExpiredReceiptReleaseDoesNotEatOtherReservations 是并发正确性缺陷的回归测试：
//
// 长请求 A 的凭据先被 TTL 回收，之后 B 建立了自己的预留，A 才结算归还。
// 旧实现只看聚合键是否存在就无条件递减，会把 B 的预留一起扣掉（护栏被错误解除，
// 并发超额窗口重开）。新实现要求凭据存在，因此 A 的归还是 no-op。
func (s *BillingReservationSuite) TestExpiredReceiptReleaseDoesNotEatOtherReservations() {
	cache, rdb := s.reservationCache()
	ctx := context.Background()
	scope := "9007"
	key := billingReservedKeyPrefix + scope

	// A 预留 0.10。
	_, err := cache.ReserveUserBalance(ctx, scope, "req-a", 0.10, 10*time.Minute)
	require.NoError(s.T(), err, "A 预留")

	// 模拟 A 的凭据被 TTL 自愈回收（聚合键仍在：保守方向，偏高不会放行超额）。
	require.NoError(s.T(), rdb.Del(ctx, billingReservedItemKey(scope, "req-a")).Err(), "回收 A 的凭据")

	// B 之后预留 0.30。
	_, err = cache.ReserveUserBalance(ctx, scope, "req-b", 0.30, 10*time.Minute)
	require.NoError(s.T(), err, "B 预留")
	total, err := cache.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(s.T(), err, "读回总额")
	require.InDelta(s.T(), 0.40, total, 1e-9, "聚合总额应为 A+B")

	// A 晚到的归还：必须 no-op，绝不能吃掉 B 的 0.30。
	err = cache.ReleaseUserBalanceReservation(ctx, scope, "req-a", 0.10, 10*time.Minute)
	require.ErrorIs(s.T(), err, service.ErrBillingReservationExpired, "过期凭据的归还必须被拒绝")
	total, err = cache.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(s.T(), err, "读回总额")
	require.InDelta(s.T(), 0.40, total, 1e-9, "过期凭据的归还不得影响他人预留")

	// B 正常归还：只剩 A 那笔已被 TTL 回收的残留份额。
	require.NoError(s.T(), cache.ReleaseUserBalanceReservation(ctx, scope, "req-b", 0.30, 10*time.Minute), "B 归还")
	total, err = cache.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(s.T(), err, "读回总额")
	require.InDelta(s.T(), 0.10, total, 1e-9, "归还只能减掉自己的份额")

	exists, err := rdb.Exists(ctx, key).Result()
	require.NoError(s.T(), err, "Exists")
	require.Equal(s.T(), int64(1), exists, "残留份额仍挂在聚合键上，等聚合键 TTL 兜底")
}

func (s *BillingReservationSuite) TestRenewExtendsReceiptTTL() {
	cache, rdb := s.reservationCache()
	ctx := context.Background()
	scope := "9008"

	_, err := cache.ReserveUserBalance(ctx, scope, "req-a", 0.10, 5*time.Minute)
	require.NoError(s.T(), err, "Reserve")

	require.NoError(s.T(), cache.RenewUserBalanceReservation(ctx, scope, "req-a", 30*time.Minute), "续期")

	itemTTL, err := rdb.TTL(ctx, billingReservedItemKey(scope, "req-a")).Result()
	require.NoError(s.T(), err, "item TTL")
	s.AssertTTLWithin(itemTTL, 25*time.Minute, 30*time.Minute)

	totalTTL, err := rdb.TTL(ctx, billingReservedKey(scope)).Result()
	require.NoError(s.T(), err, "aggregate TTL")
	s.AssertTTLWithin(totalTTL, 25*time.Minute, 30*time.Minute)

	// 凭据不存在（已归还 / 已过期）时续期必须返回"已过期"，让调用方停止心跳。
	err = cache.RenewUserBalanceReservation(ctx, scope, "req-missing", 30*time.Minute)
	require.ErrorIs(s.T(), err, service.ErrBillingReservationExpired, "凭据缺失时续期必须报告已过期")
}

func (s *BillingReservationSuite) TestConcurrentReserveAccumulatesExactly() {
	cache, rdb := s.reservationCache()
	ctx := context.Background()
	scope := "9009"

	const workers = 20
	const perWorker = 0.10

	var wg sync.WaitGroup
	totals := make([]float64, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			totals[idx], errs[idx] = cache.ReserveUserBalance(ctx, scope, fmt.Sprintf("req-%d", idx), perWorker, 10*time.Minute)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(s.T(), err, "goroutine %d 预留失败", i)
	}

	// 20 × 0.10 == 2.00：INCRBYFLOAT 的十进制累加必须精确到浮点误差内。
	final, err := cache.ReservedUserBalanceTotal(ctx, scope)
	require.NoError(s.T(), err, "读回并发累加总额")
	require.InDelta(s.T(), 2.00, final, 1e-9, "20 笔并发预留应精确累加为 2.00")

	// 快照式读取曾出现"最后一个写入者"的形态：确认累计值不是单笔值。
	require.Greater(s.T(), final, perWorker, "并发累加不得退化为单笔覆盖")

	// 并发场景下每笔都必须留下自己的凭据，否则无法安全归还。
	// （夹具的 prefixHook 不改写 KEYS 的 pattern，所以这里逐 key 单发 EXISTS 统计。）
	itemCount := 0
	for i := 0; i < workers; i++ {
		itemCount += int(s.itemExists(rdb, scope, fmt.Sprintf("req-%d", i)))
	}
	require.Equal(s.T(), workers, itemCount, "每笔并发预留都应有独立凭据")
}

// itemExists 单 key 探测某笔预留凭据是否存在（返回 1/0）。
//
// 必须单 key：夹具的 prefixHook 对 EXISTS 只改写第一个 key 参数，一次传多个 key
// 会漏改写、得到错误的计数。
func (s *BillingReservationSuite) itemExists(rdb *redis.Client, scope, requestID string) int64 {
	s.T().Helper()
	n, err := rdb.Exists(context.Background(), billingReservedItemKey(scope, requestID)).Result()
	require.NoError(s.T(), err, "Exists reservation receipt %s", requestID)
	return n
}

func TestBillingReservationSuite(t *testing.T) {
	suite.Run(t, new(BillingReservationSuite))
}
