//go:build unit

package service

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// reservationCacheStub 在余额缓存桩之上实现"在途预留"能力：用互斥锁保护的累计值
// 精确模拟 Redis 端 INCRBYFLOAT / 递减归零删除的语义，用于验证并发准入的原子性。
//
// 累计刻度为 1e-9 USD（nano）：Redis INCRBYFLOAT 走十进制累加，0.1+0.1+0.1 返回 "0.3"；
// 若这里用二进制 float64 累加会得到 0.30000000000000004，令边界判定偏离真实语义。
//
// 同时精确模拟"每请求凭据"：预留按 requestID 记账，归还只有在**该 requestID 的凭据
// 仍然存在**时才允许递减。这是修掉"晚到的归还吃掉别人的预留"的关键语义，
// expireAllReceipts() 用来模拟凭据被 TTL 自愈回收（聚合总额仍在，属保守方向）。
type reservationCacheStub struct {
	balanceEligibilityCacheStub

	mu           sync.Mutex
	receipts     map[string]map[string]float64
	reservedNano map[string]int64
	reserveCalls atomic.Int64
	releaseCalls atomic.Int64
	renewCalls   atomic.Int64
	failReserve  atomic.Bool
	failRelease  atomic.Bool
}

func (s *reservationCacheStub) ReserveUserBalance(_ context.Context, scope string, requestID string, amount float64, _ time.Duration) (float64, error) {
	s.reserveCalls.Add(1)
	if s.failReserve.Load() {
		return 0, errors.New("redis down")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.receipts == nil {
		s.receipts = make(map[string]map[string]float64)
	}
	if s.reservedNano == nil {
		s.reservedNano = make(map[string]int64)
	}
	if s.receipts[scope] == nil {
		s.receipts[scope] = make(map[string]float64)
	}
	// 幂等：同 scope + requestID 重复预留不重复累加（与 Redis 端 SET NX 语义一致）。
	if _, exists := s.receipts[scope][requestID]; !exists {
		s.receipts[scope][requestID] = amount
		s.reservedNano[scope] += int64(math.Round(amount * 1e9))
	}
	return float64(s.reservedNano[scope]) / 1e9, nil
}

func (s *reservationCacheStub) ReleaseUserBalanceReservation(_ context.Context, scope string, requestID string, _ float64, _ time.Duration) error {
	s.releaseCalls.Add(1)
	if s.failRelease.Load() {
		return errors.New("redis down")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, exists := s.receipts[scope][requestID]
	if !exists {
		// 凭据已随 TTL 过期：整体 no-op，绝不能触碰聚合总额（否则会扣掉别人的预留）。
		return ErrBillingReservationExpired
	}
	delete(s.receipts[scope], requestID)
	s.reservedNano[scope] -= int64(math.Round(stored * 1e9))
	if s.reservedNano[scope] < 0 {
		s.reservedNano[scope] = 0
	}
	return nil
}

func (s *reservationCacheStub) RenewUserBalanceReservation(_ context.Context, scope string, requestID string, _ time.Duration) error {
	s.renewCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.receipts[scope][requestID]; !exists {
		return ErrBillingReservationExpired
	}
	return nil
}

// expireAllReceipts 模拟凭据被 Redis 的 TTL 自愈回收：凭据消失，但聚合总额保持
// 不变（真实实现里聚合键只在自身 TTL 到期时才消失），因此这是"偏低风险、偏高占用"
// 的保守方向。
func (s *reservationCacheStub) expireAllReceipts() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 只清凭据：聚合总额仍旧保留在 Redis（聚合键只在自身 TTL 到期时消失），
	// 这正是“晚到的归还不得吃掉别人预留”缺陷能复现的前提。
	s.receipts = make(map[string]map[string]float64)
}

func (s *reservationCacheStub) reservedAmount() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, nano := range s.reservedNano {
		total += nano
	}
	return float64(total) / 1e9
}

func (s *reservationCacheStub) reservedAmountForScope(scope string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return float64(s.reservedNano[scope]) / 1e9
}

// atomicReservationCacheStub 建模生产路径：底层缓存实现了原子预留能力
// （repository.billingCache.TryReserveUserBalance 的 Lua 语义），越界时**不落盘**，
// 因此 service 层只需要回滚同请求已经绑定的其它 scope。
type atomicReservationCacheStub struct {
	reservationCacheStub
}

// unavailableBackendsReservationCacheStub models a cache whose Redis ledger
// failed and whose configured DB fallback also failed.
type unavailableBackendsReservationCacheStub struct {
	atomicReservationCacheStub
}

func (s *unavailableBackendsReservationCacheStub) TryReserveUserBalance(context.Context, string, string, float64, float64, time.Duration) (float64, bool, error) {
	return 0, false, ErrReservationBackendsUnavailable
}

func (s *atomicReservationCacheStub) TryReserveUserBalance(_ context.Context, scope string, requestID string, amount, maxTotal float64, _ time.Duration) (float64, bool, error) {
	s.reserveCalls.Add(1)
	if s.failReserve.Load() {
		return 0, false, errors.New("redis down")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.receipts == nil {
		s.receipts = make(map[string]map[string]float64)
	}
	if s.reservedNano == nil {
		s.reservedNano = make(map[string]int64)
	}
	if s.receipts[scope] == nil {
		s.receipts[scope] = make(map[string]float64)
	}
	current := float64(s.reservedNano[scope]) / 1e9
	// 幂等重放：同一 requestID 重复预留直接返回当前总额（对应 Lua 的 existing 分支）。
	if _, exists := s.receipts[scope][requestID]; exists {
		return current, true, nil
	}
	next := s.reservedNano[scope] + int64(math.Round(amount*1e9))
	if float64(next)/1e9 > maxTotal+1e-12 {
		return current, false, nil
	}
	s.receipts[scope][requestID] = amount
	s.reservedNano[scope] = next
	return float64(next) / 1e9, true, nil
}

func newReservationTestService(cache BillingCache) *BillingCacheService {
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	return svc
}

func newReservationTestServiceWithMultiplier(cache BillingCache, m float64) *BillingCacheService {
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	cfg.Billing.InflightReservationBudgetMultiplier = m
	return NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
}

// TestInflightReservationBudgetMultiplier_ZeroValueIsStrict 锁死零值/缺省语义：
// 未配置或 <1（含 0）一律回退 1.0，保证手工装配的 Config 与线上默认都是严格模式。
func TestInflightReservationBudgetMultiplier_ZeroValueIsStrict(t *testing.T) {
	for _, m := range []float64{0, 0.5, -1} {
		svc := newReservationTestServiceWithMultiplier(&reservationCacheStub{}, m)
		require.Equal(t, 1.0, svc.inflightReservationBudgetMultiplier(context.Background()),
			"multiplier=%v 必须回退默认 1.0（严格、零坏账）", m)
		svc.Stop()
	}
	svc := newReservationTestServiceWithMultiplier(&reservationCacheStub{}, 1000)
	require.Equal(t, 1000.0, svc.inflightReservationBudgetMultiplier(context.Background()))
	svc.Stop()

	// 完全未配置（BillingConfig 零值）同样是严格模式。
	svc = newReservationTestService(&reservationCacheStub{})
	require.Equal(t, 1.0, svc.inflightReservationBudgetMultiplier(context.Background()))
	svc.Stop()
}

// TestInflightReservationAllowed_DefaultStrictVsRelaxed 用 10 万 token 上下文的真实数字
// 对比两种准入语义：余额 $1.0、封底 $0.10、单笔最坏费用 $0.075。
//
//   - 严格（默认 1.0）：预算 = 可花余额 0.90，最多同时放行 12 笔；第 13 笔起被拒。
//   - 放宽（1000）：预算 = 900，200 个并发全部放行，停止点交给「单笔最坏费用」闸门
//     与结算封底 —— 也就是「余额花到 0.1 才拒」。
func TestInflightReservationAllowed_DefaultStrictVsRelaxed(t *testing.T) {
	const (
		balance = 1.0
		reserve = 0.10
		worst   = 0.075
	)
	strict := newReservationTestServiceWithMultiplier(&reservationCacheStub{}, 0)
	defer strict.Stop()
	relaxed := newReservationTestServiceWithMultiplier(&reservationCacheStub{}, 1000)
	defer relaxed.Stop()
	ctx := context.Background()

	// 严格模式：0.90 / 0.075 == 12 笔是上限。
	require.True(t, strict.inflightReservationAllowed(ctx, balance, 12*worst, worst),
		"严格模式：第 12 笔仍在可花余额内")
	require.False(t, strict.inflightReservationAllowed(ctx, balance, 13*worst, worst),
		"严格模式：第 13 笔超预算，必须拒绝")
	require.False(t, strict.inflightReservationAllowed(ctx, balance, 200*worst, worst),
		"严格模式：200 并发远超预算，必须拒绝")

	// 放宽模式：200 并发全部放行。
	require.True(t, relaxed.inflightReservationAllowed(ctx, balance, 200*worst, worst),
		"放宽模式：200 个并发预留应全部放行（余额花到封底为止）")

	// 两种模式都至少放行一笔，避免退化时第一笔被自己挡住。
	require.True(t, relaxed.inflightReservationAllowed(ctx, reserve+worst, worst, worst))
	require.True(t, strict.inflightReservationAllowed(ctx, reserve+worst, worst, worst))
	require.True(t, relaxed.inflightReservationAllowed(ctx, balance, worst, worst))
}

// TestReserveRequestSpend_RelaxedBudgetAdmitsAllConcurrency 端到端验证放宽后的并发准入：
// 余额 1.0、封底 0.1、单笔最坏费用 0.075，200 个并发请求必须**全部**放行，
// 而不是严格模式下的 12 笔。
func TestReserveRequestSpend_RelaxedBudgetAdmitsAllConcurrency(t *testing.T) {
	cache := &reservationCacheStub{}
	cache.balance = 1.0
	svc := newReservationTestServiceWithMultiplier(cache, 1000)
	t.Cleanup(svc.Stop)

	const attempts = 200
	start := make(chan struct{})
	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slot := new(BillingReservationSlot)
			<-start
			results <- svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "",
				reservationEligibilityOpts(slot, 0.075)...)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var admitted int
	for err := range results {
		if err == nil {
			admitted++
			continue
		}
		require.ErrorIs(t, err, ErrInsufficientBalance)
	}
	require.Equal(t, attempts, admitted, "放宽模式：200 并发必须全部放行")
	require.InDelta(t, attempts*0.075, cache.reservedAmount(), 1e-6, "在途预留总额 = 200 * 0.075")
}

func reservationEligibilityOpts(slot *BillingReservationSlot, worstSpend float64) []BillingEligibilityOption {
	return []BillingEligibilityOption{
		WithMaxRequestSpend(worstSpend),
		WithBalanceReservation(slot),
	}
}

// TestReserveRequestSpend_ConcurrentAdmissionIsAtomic 复现并锁死并发穿透：
// 余额 0.45、封底 0.10、每笔最坏费用 0.10 时，10 个并发请求只能放行 3 笔
// （0.45 - 3*0.10 == 0.15 > 0.10，第 4 笔起 0.45 - 0.4 == 0.05 < 0.10），
// 其余必须在预检即 403。
//
// 修复前：10 笔都读到同一份余额快照 0.45（10*0.10 远超 0.45 也照样全放行），
// 结算时只有前几笔扣得动，其余全部 actual_cost=0 —— 坏账。
func TestReserveRequestSpend_ConcurrentAdmissionIsAtomic(t *testing.T) {
	cache := &reservationCacheStub{}
	cache.balance = 0.45
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	const attempts = 10
	start := make(chan struct{})
	results := make(chan error, attempts)
	heldSlots := make(chan *BillingReservationSlot, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slot := new(BillingReservationSlot)
			<-start
			err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(slot, 0.10)...)
			if err == nil {
				heldSlots <- slot
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(heldSlots)

	var admitted, rejected int
	for err := range results {
		switch {
		case err == nil:
			admitted++
		case errors.Is(err, ErrInsufficientBalance):
			rejected++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	require.Equal(t, 3, admitted, "只有 3 笔能守住封底：0.45 - 3*0.10 == 0.15")
	require.Equal(t, attempts-3, rejected, "其余并发请求必须在预检被 403")
	require.InDelta(t, 0.30, cache.reservedAmount(), 1e-9, "在途预留总额应为 3*0.10")
	require.Equal(t, int64(7), cache.releaseCalls.Load(), "被拒绝的 7 笔必须已回滚预留")

	var held []*BillingReservationSlot
	for slot := range heldSlots {
		held = append(held, slot)
	}
	require.Len(t, held, 3)

	// 归还一笔后腾出额度（0.45 - 0.20 == 0.25 > 0.10）：下一笔立即可以放行。
	held[0].Release(context.Background())
	require.InDelta(t, 0.20, cache.reservedAmount(), 1e-9)
	require.Equal(t, int64(8), cache.releaseCalls.Load())

	var slot BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&slot, 0.10)...))
	require.InDelta(t, 0.30, cache.reservedAmount(), 1e-9)

	// 再打一笔：护栏不成立（0.45 - 0.40 == 0.05 < 0.10），必须回滚预留并 403。
	var overflow BillingReservationSlot
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&overflow, 0.10)...)
	require.ErrorIs(t, err, ErrInsufficientBalance)
	require.InDelta(t, 0.30, cache.reservedAmount(), 1e-9, "被拒绝的预留必须已回滚")
	require.Equal(t, int64(9), cache.releaseCalls.Load(), "第 9 次 release 是被拒绝预留的回滚")
}

// TestReserveRequestSpend_FailsOpenWhenReservationUnavailable 确认预留能力故障
// 时 fail-open：不影响既有余额闸门的判定，也不会误绑槽位。
func TestReserveRequestSpend_FailsOpenWhenReservationUnavailable(t *testing.T) {
	cache := &reservationCacheStub{}
	cache.balance = 0.45
	cache.failReserve.Store(true)
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	var slot BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&slot, 0.10)...))

	// 预留失败 → 槽位未绑定 → 收尾归还必须是 no-op（不能去动别人的预留）。
	slot.ReleaseOnExit(context.Background())
	require.Equal(t, int64(0), cache.releaseCalls.Load())
	require.InDelta(t, 0.0, cache.reservedAmount(), 1e-9)
}

// TestReserveRequestSpend_FailsClosedWhenRedisAndDBUnavailable verifies the
// production safety boundary: when both reservation ledgers are unavailable,
// the preflight must return HTTP 503 instead of admitting an unguarded request.
func TestReserveRequestSpend_FailsClosedWhenRedisAndDBUnavailable(t *testing.T) {
	cache := &unavailableBackendsReservationCacheStub{}
	cache.balance = 0.45
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	before := BillingGuardStatsSnapshot().ReservationFailClosed
	var slot BillingReservationSlot
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&slot, 0.10)...)
	require.ErrorIs(t, err, ErrBillingServiceUnavailable)
	require.Equal(t, before+1, BillingGuardStatsSnapshot().ReservationFailClosed)
	require.Equal(t, int64(0), cache.releaseCalls.Load())
}

// TestReserveRequestSpend_NoopForPlainCacheStub 确认没有实现预留能力的缓存
// （历史测试桩 / 降级装配）依旧走原来的行为：不预留、不阻断。
func TestReserveRequestSpend_NoopForPlainCacheStub(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 0.40}
	// 普通缓存桩未实现预留能力：预检不得被预留逻辑阻断。
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	var slot BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&slot, 0.10)...))
	slot.ReleaseOnExit(context.Background())
}

// TestReserveRequestSpend_NoopWithoutWorstSpend 确认没有最坏费用上界（未挂
// WithMaxRequestSpend）时不产生预留：预留必须与放行上界同源，否则会凭空占额度。
func TestReserveRequestSpend_NoopWithoutWorstSpend(t *testing.T) {
	cache := &reservationCacheStub{}
	cache.balance = 0.45
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	var slot BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", WithBalanceReservation(&slot)))
	require.Equal(t, int64(0), cache.reserveCalls.Load())
	require.InDelta(t, 0.0, cache.reservedAmount(), 1e-9)
}

// TestBillingReservationSlot_HandOffDefersReleaseToSettlement 验证归还时机：
// 已提交结算任务的请求，收尾兜底（ReleaseOnExit）不得提前归还；只有结算任务
// 的 Release 才真正归还，且重复调用幂等。
func TestBillingReservationSlot_HandOffDefersReleaseToSettlement(t *testing.T) {
	cache := &reservationCacheStub{}
	cache.balance = 0.45
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	var slot BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&slot, 0.10)...))
	require.InDelta(t, 0.10, cache.reservedAmount(), 1e-9)

	// handler 收尾：已移交给结算任务（先 HandOff），此时不允许归还。
	slot.HandOff()
	slot.ReleaseOnExit(context.Background())
	require.Equal(t, int64(0), cache.releaseCalls.Load(), "移交结算后收尾兜底不得提前归还")
	require.InDelta(t, 0.10, cache.reservedAmount(), 1e-9)

	// 结算任务在扣费完成后归还；重复调用幂等。
	slot.Release(context.Background())
	slot.Release(context.Background())
	require.Equal(t, int64(1), cache.releaseCalls.Load(), "Release 必须幂等")
	require.InDelta(t, 0.0, cache.reservedAmount(), 1e-9)
}

// TestBillingReservationSlot_ReleaseOnExitWithoutSettlement 验证没有结算的
// 错误路径：收尾兜底立即归还，不把额度钉到 TTL 到期。
func TestBillingReservationSlot_ReleaseOnExitWithoutSettlement(t *testing.T) {
	cache := &reservationCacheStub{}
	cache.balance = 0.45
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	var slot BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&slot, 0.10)...))

	slot.ReleaseOnExit(context.Background())
	require.Equal(t, int64(1), cache.releaseCalls.Load())
	require.InDelta(t, 0.0, cache.reservedAmount(), 1e-9)

	// 再做一次兜底（例如 defer 与显式归还同时存在）不得重复扣减。
	slot.ReleaseOnExit(context.Background())
	slot.Release(context.Background())
	require.Equal(t, int64(1), cache.releaseCalls.Load())
	require.InDelta(t, 0.0, cache.reservedAmount(), 1e-9)
}

// TestReleaseReservation_ExpiredReceiptDoesNotEatOtherReservations 是针对
// "晚到的归还吃掉别人预留"这条并发正确性缺陷的回归测试。
//
// 场景（真实可达）：请求 A 的在线时间超过预留 TTL（长流式响应），它的凭据先被
// Redis 的 TTL 自愈回收；此后请求 B 建立了自己的预留；A 最终结算并归还。
//
// 旧实现只看"聚合键是否存在"就无条件递减，于是 A 的归还把 B 的预留一起扣掉，
// 护栏被错误解除，并发超额窗口重新打开。新实现要求凭据仍在，凭据没了就整体 no-op。
func TestReleaseReservation_ExpiredReceiptDoesNotEatOtherReservations(t *testing.T) {
	cache := &reservationCacheStub{}
	cache.balance = 1.00
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	ctx := context.Background()

	// A 放行并持有 0.10 预留。
	var slotA BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(ctx, &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&slotA, 0.10)...))
	require.InDelta(t, 0.10, cache.reservedAmount(), 1e-9)

	// A 的凭据因超过 TTL 被自愈回收（聚合总额仍在：这是保守方向，偏高不会放行超额）。
	cache.expireAllReceipts()

	// B 随后放行，建立 0.30 的预留。
	var slotB BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(ctx, &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&slotB, 0.30)...))
	require.InDelta(t, 0.40, cache.reservedAmount(), 1e-9)

	// A 晚到的归还是 no-op：绝不能把 B 的 0.30 一起减掉（旧实现会减到 0.10）。
	slotA.Release(ctx)
	require.InDelta(t, 0.40, cache.reservedAmount(), 1e-9, "凭据已过期的归还不得影响他人的预留")

	// B 正常归还：只剩 A 那笔已被 TTL 回收的残留份额。
	slotB.Release(ctx)
	require.InDelta(t, 0.10, cache.reservedAmount(), 1e-9, "归还只能减掉自己的份额")
}

// TestBillingReservationSlot_HeartbeatRenewsAndStopsOnRelease 验证长请求心跳：
// 预留会被周期性续期（避免超长请求在结算前丢掉护栏），归还后心跳必须停止。
func TestBillingReservationSlot_HeartbeatRenewsAndStopsOnRelease(t *testing.T) {
	oldInterval := billingReservationHeartbeatInterval
	billingReservationHeartbeatInterval = 5 * time.Millisecond
	t.Cleanup(func() { billingReservationHeartbeatInterval = oldInterval })

	cache := &reservationCacheStub{}
	cache.balance = 1.00
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	var slot BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&slot, 0.10)...))

	require.Eventually(t, func() bool { return cache.renewCalls.Load() >= 1 },
		time.Second, 5*time.Millisecond, "长请求预留必须被心跳续期")

	slot.Release(context.Background())
	time.Sleep(20 * time.Millisecond)
	settled := cache.renewCalls.Load()
	time.Sleep(40 * time.Millisecond)
	require.Equal(t, settled, cache.renewCalls.Load(), "归还后心跳必须停止，不得让凭据复活")
}

// subscriptionReservationCacheStub 在预留桩之上提供一份"有效订阅 + 用量可设"的
// 订阅缓存数据，用于验证订阅模式的在途预留。
type subscriptionReservationCacheStub struct {
	reservationCacheStub
	subData *SubscriptionCacheData
}

func (s *subscriptionReservationCacheStub) GetSubscriptionCache(context.Context, int64, int64) (*SubscriptionCacheData, error) {
	return s.subData, nil
}

// TestSubscriptionReservation_LimitsConcurrentOversell 验证订阅模式的并发超卖被堵住：
// 日限额 1.00、已用 0.60，每笔最坏费用 0.20 时只放行 2 笔
// （0.60 + 2*0.20 == 1.00 —— 最坏情况全部花光也正好落在限额上，不越界；第 3 笔
// 0.60 + 3*0.20 == 1.20 > 1.00 必须被拒）。
//
// 修复前：订阅模式完全没有在途预留，N 笔并发都读到同一份"已用 0.60"的用量快照，
// 全部放行，配额被超额消耗（超卖）。
func TestSubscriptionReservation_LimitsConcurrentOversell(t *testing.T) {
	dailyLimit := 1.00
	cache := &subscriptionReservationCacheStub{
		subData: &SubscriptionCacheData{
			Status:     SubscriptionStatusActive,
			ExpiresAt:  time.Now().Add(time.Hour),
			DailyUsage: 0.60,
		},
	}
	cache.balance = 100 // 余额模式的额度与本用例无关

	cfg := &config.Config{}
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	group := &Group{ID: 10, SubscriptionType: "subscription", Status: "active", DailyLimitUSD: &dailyLimit}
	subscription := &UserSubscription{Status: "active"}
	user := &User{ID: 42}

	const attempts = 5
	start := make(chan struct{})
	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slot := new(BillingReservationSlot)
			<-start
			results <- svc.CheckBillingEligibility(context.Background(), user, nil, group, subscription, "", reservationEligibilityOpts(slot, 0.20)...)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var admitted, rejected int
	for err := range results {
		switch {
		case err == nil:
			admitted++
		case errors.Is(err, ErrDailyLimitExceeded):
			rejected++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	require.Equal(t, 2, admitted, "只有 2 笔能在日限额内：0.60 + 2*0.20 == 1.00")
	require.Equal(t, attempts-2, rejected, "超出剩余限额的请求必须在预检被拒")
	require.InDelta(t, 0.40, cache.reservedAmount(), 1e-9, "在途预留应为 2*0.20")
}

// TestSubscriptionReservation_NoopWhenNoLimits 确认不限量的订阅（三个限额都未配置）
// 不产生任何预留，行为与修复前一致。
func TestSubscriptionReservation_NoopWhenNoLimits(t *testing.T) {
	cache := &subscriptionReservationCacheStub{
		subData: &SubscriptionCacheData{
			Status:    SubscriptionStatusActive,
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}
	cfg := &config.Config{}
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	group := &Group{ID: 10, SubscriptionType: "subscription", Status: "active"}
	var slot BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 42}, nil, group, &UserSubscription{Status: "active"}, "", reservationEligibilityOpts(&slot, 0.20)...))
	require.Equal(t, int64(0), cache.reserveCalls.Load(), "无限额订阅不得产生预留")
}

// TestSubscriptionReservation_MissingWorstSpendKeepsOldBehaviour 确认没有最坏费用上界时
// 订阅模式退回既有判定（不预留、不额外拦截）。
func TestSubscriptionReservation_MissingWorstSpendKeepsOldBehaviour(t *testing.T) {
	dailyLimit := 1.00
	cache := &subscriptionReservationCacheStub{
		subData: &SubscriptionCacheData{
			Status:     SubscriptionStatusActive,
			ExpiresAt:  time.Now().Add(time.Hour),
			DailyUsage: 0.60,
		},
	}
	cfg := &config.Config{}
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	group := &Group{ID: 10, SubscriptionType: "subscription", Status: "active", DailyLimitUSD: &dailyLimit}
	var slot BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 42}, nil, group, &UserSubscription{Status: "active"}, "", WithBalanceReservation(&slot)))
	require.Equal(t, int64(0), cache.reserveCalls.Load(), "无最坏费用上界时不预留")
}

func newPlatformQuotaReservationTestService(cache BillingCache, rec *UserPlatformQuotaRecord) *BillingCacheService {
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	svc.userPlatformQuotaRepo = &fakeQuotaRepo{rec: rec}
	return svc
}

// TestPlatformQuotaReservation_BlocksConcurrentOversell 锁死 user×platform 配额的
// 并发超卖：daily limit=0.30、每笔最坏费用 0.10 时，前 3 笔能把限额正好用满，
// 第 4 笔必须在预检被拒；且第 4 笔先绑定的余额预留必须随拒绝一起回滚。
func TestPlatformQuotaReservation_BlocksConcurrentOversell(t *testing.T) {
	cache := &reservationCacheStub{balance: 10.0}
	dailyLimit := 0.30
	svc := newPlatformQuotaReservationTestService(cache, &UserPlatformQuotaRecord{
		UserID: 1, Platform: "anthropic", DailyLimitUSD: &dailyLimit,
	})
	t.Cleanup(svc.Stop)

	slots := make([]*BillingReservationSlot, 0, 3)
	for i := 0; i < 3; i++ {
		slot := &BillingReservationSlot{}
		err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "anthropic", reservationEligibilityOpts(slot, 0.10)...)
		require.NoError(t, err, "第 %d 笔应通过（限额可被最坏费用正好用满）", i+1)
		slots = append(slots, slot)
	}
	require.InDelta(t, 0.60, cache.reservedAmount(), 1e-9, "3 笔请求各占余额 + 平台两条 0.10 凭据")

	overflow := &BillingReservationSlot{}
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "anthropic", reservationEligibilityOpts(overflow, 0.10)...)
	require.ErrorIs(t, err, ErrUserPlatformDailyQuotaExhausted)
	require.InDelta(t, 0.60, cache.reservedAmount(), 1e-9, "被拒请求的余额预留必须回滚")
	// 该桩未实现原子预留：越界的平台预留先落盘、再由护栏回滚，加上余额绑定的
	// 回滚，第 4 笔一共释放 2 次（生产路径见下一个用例）。
	require.Equal(t, int64(2), cache.releaseCalls.Load(), "第 4 笔的余额绑定与平台预留都必须回滚")

	for _, slot := range slots {
		slot.Release(context.Background())
	}
	require.InDelta(t, 0.0, cache.reservedAmount(), 1e-9)
	require.Equal(t, int64(8), cache.releaseCalls.Load(), "2 次回滚 + 3 笔 × 2 个 scope")
}

// TestPlatformQuotaReservation_AtomicStoreLeavesNoResidue 锁死生产路径（底层缓存实现
// TryReserveUserBalance 原子预留）上的拒绝语义：越界的平台预留在 Lua 里就被挡下、
// 根本不会落盘，拒绝只需回滚同请求已绑定的余额预留，在途总额保持不变。
func TestPlatformQuotaReservation_AtomicStoreLeavesNoResidue(t *testing.T) {
	cache := &atomicReservationCacheStub{}
	cache.balance = 10.0
	dailyLimit := 0.30
	svc := newPlatformQuotaReservationTestService(cache, &UserPlatformQuotaRecord{
		UserID: 1, Platform: "anthropic", DailyLimitUSD: &dailyLimit,
	})
	t.Cleanup(svc.Stop)

	platformScope := userPlatformQuotaReservationScope(1, "anthropic")

	slots := make([]*BillingReservationSlot, 0, 3)
	for i := 0; i < 3; i++ {
		slot := &BillingReservationSlot{}
		err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "anthropic", reservationEligibilityOpts(slot, 0.10)...)
		require.NoError(t, err, "第 %d 笔应通过（限额可被最坏费用正好用满）", i+1)
		slots = append(slots, slot)
	}
	require.InDelta(t, 0.30, cache.reservedAmountForScope(platformScope), 1e-9, "平台 scope 恰好用满限额")
	require.InDelta(t, 0.60, cache.reservedAmount(), 1e-9)

	overflow := &BillingReservationSlot{}
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "anthropic", reservationEligibilityOpts(overflow, 0.10)...)
	require.ErrorIs(t, err, ErrUserPlatformDailyQuotaExhausted)
	require.InDelta(t, 0.30, cache.reservedAmountForScope(platformScope), 1e-9, "原子拒绝不得留下平台预留残留")
	require.InDelta(t, 0.60, cache.reservedAmount(), 1e-9, "第 4 笔的余额预留必须随拒绝回滚")
	require.Equal(t, int64(1), cache.releaseCalls.Load(), "原子拒绝只需回滚余额绑定一次")

	for _, slot := range slots {
		slot.Release(context.Background())
	}
	require.InDelta(t, 0.0, cache.reservedAmount(), 1e-9)
	require.Equal(t, int64(7), cache.releaseCalls.Load(), "1 次回滚 + 3 笔 × 2 个 scope")
}

// TestPlatformQuotaReservation_UsesTightestWindow 验证三个窗口取最紧的那个：
// daily 宽松、weekly 只够 2 笔时，第 3 笔必须以 weekly 错误拒绝。
func TestPlatformQuotaReservation_UsesTightestWindow(t *testing.T) {
	cache := &reservationCacheStub{balance: 10.0}
	dailyLimit := 5.0
	weeklyLimit := 0.20
	svc := newPlatformQuotaReservationTestService(cache, &UserPlatformQuotaRecord{
		UserID: 1, Platform: "anthropic", DailyLimitUSD: &dailyLimit, WeeklyLimitUSD: &weeklyLimit,
	})
	t.Cleanup(svc.Stop)

	for i := 0; i < 2; i++ {
		slot := &BillingReservationSlot{}
		require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "anthropic", reservationEligibilityOpts(slot, 0.10)...))
	}

	overflow := &BillingReservationSlot{}
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "anthropic", reservationEligibilityOpts(overflow, 0.10)...)
	require.ErrorIs(t, err, ErrUserPlatformWeeklyQuotaExhausted)
}

// TestPlatformQuotaReservation_HandOffReleasesBothScopes 验证多绑定槽位的归还时机：
// 移交给结算任务后收尾兜底不得提前归还；结算完成时的 Release 必须归还全部 scope。
func TestPlatformQuotaReservation_HandOffReleasesBothScopes(t *testing.T) {
	cache := &reservationCacheStub{balance: 10.0}
	dailyLimit := 1.0
	svc := newPlatformQuotaReservationTestService(cache, &UserPlatformQuotaRecord{
		UserID: 1, Platform: "anthropic", DailyLimitUSD: &dailyLimit,
	})
	t.Cleanup(svc.Stop)

	slot := &BillingReservationSlot{}
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "anthropic", reservationEligibilityOpts(slot, 0.10)...))
	require.InDelta(t, 0.20, cache.reservedAmount(), 1e-9)

	slot.HandOff()
	slot.ReleaseOnExit(context.Background())
	require.InDelta(t, 0.20, cache.reservedAmount(), 1e-9, "移交结算后收尾兜底不得提前归还")

	slot.Release(context.Background())
	require.InDelta(t, 0.0, cache.reservedAmount(), 1e-9)
	require.Equal(t, int64(2), cache.releaseCalls.Load(), "两个 scope 都必须归还")
}

// TestPlatformQuotaReservation_NoopWithoutLimits 确认没有配置平台限额时
// 只产生余额预留，不写无意义的平台预留键。
func TestPlatformQuotaReservation_NoopWithoutLimits(t *testing.T) {
	cache := &reservationCacheStub{balance: 10.0}
	svc := newPlatformQuotaReservationTestService(cache, &UserPlatformQuotaRecord{UserID: 1, Platform: "anthropic"})
	t.Cleanup(svc.Stop)

	var slot BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "anthropic", reservationEligibilityOpts(&slot, 0.10)...))
	require.Equal(t, int64(1), cache.reserveCalls.Load(), "无 limit 时不产生平台预留")
	require.InDelta(t, 0.10, cache.reservedAmount(), 1e-9)
}

// TestAPIKeyQuotaReservation_BlocksConcurrentOversell 锁死 API Key 总额度（quota）的
// 并发超额：quota=0.30、每笔最坏费用 0.10 时前 3 笔正好用满，第 4 笔必须在预检被
// 429 拒绝，且它先绑定的余额预留必须随拒绝一起回滚（与 user×platform 预留同构）。
//
// 修复前：key 额度只在鉴权时读 quota_used 快照（quota_used 又只在结算时递增），
// 并发请求会一起穿过 `quota_used >= quota` 判定，使该 key 超额消耗。
func TestAPIKeyQuotaReservation_BlocksConcurrentOversell(t *testing.T) {
	cache := &reservationCacheStub{balance: 10.0}
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	apiKey := &APIKey{ID: 7, UserID: 1, Quota: 0.30}
	keyScope := apiKeyQuotaReservationScope(apiKey.ID)

	slots := make([]*BillingReservationSlot, 0, 3)
	for i := 0; i < 3; i++ {
		slot := &BillingReservationSlot{}
		err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, apiKey, nil, nil, "", reservationEligibilityOpts(slot, 0.10)...)
		require.NoError(t, err, "第 %d 笔应通过（key 额度可被最坏费用正好用满）", i+1)
		slots = append(slots, slot)
	}
	require.InDelta(t, 0.30, cache.reservedAmountForScope(keyScope), 1e-9, "key 额度 scope 恰好用满")
	require.InDelta(t, 0.60, cache.reservedAmount(), 1e-9, "3 笔余额 + 3 笔 key 额度")

	overflow := &BillingReservationSlot{}
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, apiKey, nil, nil, "", reservationEligibilityOpts(overflow, 0.10)...)
	require.ErrorIs(t, err, ErrAPIKeyQuotaExhausted)
	require.InDelta(t, 0.30, cache.reservedAmountForScope(keyScope), 1e-9, "被拒请求不得留下 key 额度残留")
	require.InDelta(t, 0.60, cache.reservedAmount(), 1e-9, "第 4 笔的余额预留必须随拒绝回滚")

	for _, slot := range slots {
		slot.Release(context.Background())
	}
	require.InDelta(t, 0.0, cache.reservedAmount(), 1e-9)
}

// TestAPIKeyQuotaReservation_NoopForUnlimitedKey 确认不限量的 key（quota<=0）
// 不产生预留，行为与修复前一致。
func TestAPIKeyQuotaReservation_NoopForUnlimitedKey(t *testing.T) {
	cache := &reservationCacheStub{balance: 10.0}
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	var slot BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, &APIKey{ID: 7, Quota: 0}, nil, nil, "", reservationEligibilityOpts(&slot, 0.10)...))
	require.InDelta(t, 0.10, cache.reservedAmount(), 1e-9, "只应有余额预留")
	require.InDelta(t, 0.0, cache.reservedAmountForScope(apiKeyQuotaReservationScope(7)), 1e-9)
}

// TestAPIKeyQuotaReservation_RollsBackSubscriptionBindingOnReject 验证第三个 scope
// （key 额度）在订阅模式下同样生效：订阅预留已经绑定后 key 额度被拒，整槽必须回滚，
// 不得留下任何一条占着额度到 TTL 的残留凭据。
func TestAPIKeyQuotaReservation_RollsBackSubscriptionBindingOnReject(t *testing.T) {
	dailyLimit := 5.0
	cache := &subscriptionReservationCacheStub{
		subData: &SubscriptionCacheData{
			Status:    SubscriptionStatusActive,
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}
	cache.balance = 100 // 余额模式的额度与本用例无关

	cfg := &config.Config{}
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	group := &Group{ID: 10, SubscriptionType: "subscription", Status: "active", DailyLimitUSD: &dailyLimit}
	subscription := &UserSubscription{Status: "active"}
	// key 只剩 0.05，单笔最坏费用 0.10 已经吃不消。
	apiKey := &APIKey{ID: 9, UserID: 1, Quota: 0.05}

	var slot BillingReservationSlot
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, apiKey, group, subscription, "", reservationEligibilityOpts(&slot, 0.10)...)
	require.ErrorIs(t, err, ErrAPIKeyQuotaExhausted)
	require.InDelta(t, 0.0, cache.reservedAmount(), 1e-9, "被拒请求不得留下订阅或 key 额度预留")
}
