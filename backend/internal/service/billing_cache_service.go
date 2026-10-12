package service

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

// 错误定义
// 注：ErrInsufficientBalance在redeem_service.go中定义
// 注：ErrDailyLimitExceeded/ErrWeeklyLimitExceeded/ErrMonthlyLimitExceeded在subscription_service.go中定义
// errBillingCacheUnavailable 内部哨兵：用于 quota 校验路径在 cache==nil 时
// 与"Redis 故障"走同一条 fail-open + DB 一次性检查的分支。
var errBillingCacheUnavailable = fmt.Errorf("billing cache unavailable")

// ErrBillingReservationExpired 表示"本请求的在途预留凭据已随 TTL 过期"。
//
// 这是**正常且期望**的结果，不是故障：预留 TTL 的作用就是在结算任务丢失 / 进程
// 崩溃时自愈。仓库层在归还脚本发现凭据不存在时返回它，服务层据此跳过递减并计入
// ReservationExpiredRelease 指标 —— 语义是"这块额度已经由 TTL 回收，本次归还被
// 安全忽略"，绝不能因此去递减聚合总额（那会扣掉别人的预留）。
var ErrBillingReservationExpired = fmt.Errorf("billing reservation already expired")

// ErrReservationBackendsUnavailable 表示 Redis 预留账本不可用，且已装配的 DB
// 兜底账本同样不可用。此时不能退回无预留的 fail-open，否则并发准入护栏会整层
// 消失；服务层必须把它转成 ErrBillingServiceUnavailable（HTTP 503）。
var ErrReservationBackendsUnavailable = errors.New("billing reservation backends unavailable")

var (
	ErrSubscriptionInvalid       = infraerrors.Forbidden("SUBSCRIPTION_INVALID", "subscription is invalid or expired")
	ErrBillingServiceUnavailable = infraerrors.ServiceUnavailable("BILLING_SERVICE_ERROR", "Billing service temporarily unavailable. Please retry later.")
	// RPM 超限错误。gateway_handler 负责映射为 HTTP 429。
	ErrGroupRPMExceeded = infraerrors.TooManyRequests("GROUP_RPM_EXCEEDED", "group requests-per-minute limit exceeded")
	ErrUserRPMExceeded  = infraerrors.TooManyRequests("USER_RPM_EXCEEDED", "user requests-per-minute limit exceeded")

	// user × platform quota（HTTP 429 Too Many Requests + Retry-After header）。
	// 选用 429 而非 403：限额耗尽属于"暂时性资源用尽，重试可恢复"的场景（RFC 6585），
	// 大量 SDK（如 OpenAI 兼容客户端）只对 429 触发自动退避并读取 Retry-After，
	// 用 403 会被视为"权限不足，重试无意义"导致客户端直接报错且不退避。
	ErrUserPlatformDailyQuotaExhausted   = infraerrors.TooManyRequests("USER_PLATFORM_DAILY_QUOTA_EXHAUSTED", "Daily usage quota exhausted for this platform.")
	ErrUserPlatformWeeklyQuotaExhausted  = infraerrors.TooManyRequests("USER_PLATFORM_WEEKLY_QUOTA_EXHAUSTED", "Weekly usage quota exhausted for this platform.")
	ErrUserPlatformMonthlyQuotaExhausted = infraerrors.TooManyRequests("USER_PLATFORM_MONTHLY_QUOTA_EXHAUSTED", "Monthly usage quota exhausted for this platform.")
)

// subscriptionCacheData 订阅缓存数据结构（内部使用）
type subscriptionCacheData struct {
	Status       string
	ExpiresAt    time.Time
	DailyUsage   float64
	WeeklyUsage  float64
	MonthlyUsage float64
	Version      int64
}

// 缓存写入任务类型
type cacheWriteKind int

const (
	cacheWriteSetBalance cacheWriteKind = iota
	cacheWriteSetSubscription
	cacheWriteUpdateSubscriptionUsage
	cacheWriteDeductBalance
	cacheWriteUpdateRateLimitUsage
)

// 异步缓存写入工作池配置
//
// 性能优化说明：
// 原实现在请求热路径中使用 goroutine 异步更新缓存，存在以下问题：
// 1. 每次请求创建新 goroutine，高并发下产生大量短生命周期 goroutine
// 2. 无法控制并发数量，可能导致 Redis 连接耗尽
// 3. goroutine 创建/销毁带来额外开销
//
// 新实现使用固定大小的工作池：
// 1. 预创建 10 个 worker goroutine，避免频繁创建销毁
// 2. 使用带缓冲的 channel（1000）作为任务队列，平滑写入峰值
// 3. 非阻塞写入，队列满时关键任务同步回退，非关键任务丢弃并告警
// 4. 统一超时控制，避免慢操作阻塞工作池
const (
	cacheWriteWorkerCount     = 10              // 工作协程数量
	cacheWriteBufferSize      = 1000            // 任务队列缓冲大小
	cacheWriteTimeout         = 2 * time.Second // 单个写入操作超时
	cacheWriteDropLogInterval = 5 * time.Second // 丢弃日志节流间隔
	balanceLoadTimeout        = 3 * time.Second
	balanceRecheckTimeout     = 3 * time.Second // 预检 DB 复核超时（singleflight 共享）
)

// cacheWriteTask 缓存写入任务
type cacheWriteTask struct {
	kind             cacheWriteKind
	userID           int64
	groupID          int64
	apiKeyID         int64
	balance          float64
	amount           float64
	subscriptionData *subscriptionCacheData
	// balanceGeneration 是**发起 DB 读取之前**取到的余额变动代号（审计 R9）。
	// 只有它仍等于写入时的代号，余额快照才允许发布；否则说明读取方手里的值可能
	// 早于最近一次扣费/加钱，发布它会让预检依据偏高的余额放行。
	// 空串表示"未取到代号"（降级装配 / 轻量 stub 缓存），退回无条件发布。
	balanceGeneration string
	// subscriptionGeneration 同理，作用于订阅缓存（用量同步累加会递增它）。
	// 空串表示"未取到代号"，退回无条件发布。
	subscriptionGeneration string
}

// balancePublishGuard 是余额缓存回源写回的**可选**能力：支持"读到余额之后没有发生过
// 余额变动才发布"的条件写回（审计 R9）。
//
// 未实现该能力的缓存（测试用的轻量 stub、旧实现）自动降级为无条件写回，
// 行为与修复前一致 —— 只降低防护强度，不会产生错误结果。
type balancePublishGuard interface {
	// BalanceGeneration 返回当前的余额变动代号（键不存在时归一化为 "0"）。
	BalanceGeneration(ctx context.Context, userID int64) (string, error)
	// SetUserBalanceIfGeneration 仅在代号未变时发布快照，返回是否发布成功。
	SetUserBalanceIfGeneration(ctx context.Context, userID int64, balance float64, generation string) (bool, error)
}

// apiKeyRateLimitLoader defines the interface for loading rate limit data from DB.
type apiKeyRateLimitLoader interface {
	GetRateLimitData(ctx context.Context, keyID int64) (*APIKeyRateLimitData, error)
}

// balanceExhaustionStore 是 BillingCache 的可选扩展能力：记录"该用户的钱包已经没有
// 可花余额（已扣到 reserve 底线，或扣费被原子拒绝）"。
//
// 为什么需要它：计费是**后付费**，唯一的准入闸门是转发前的预检，而预检读的是 Redis
// 余额缓存。扣费结束后我们只做 InvalidateUserBalance(DEL)，但余额缓存的写入是
// "未命中回源 + 异步写回"：扣费**前**读到的旧余额可能在 DEL **之后**才落盘，把偏高的
// 旧值"复活"进缓存；此后预检持续放行，而上游已经被调用（成本已经发生），结算必然
// 失败（balance <= floor）——于是 usage_log.actual_cost 记 0、余额不再下降，表现为
// "余额扣不动、token 照统计、请求不阻断"，窗口最长等于余额缓存的 TTL。
//
// 这个标记只由"结算判定钱包已耗尽"写入、只在余额增加时清除；余额未命中回源的写回
// 路径永远不会写它，所以它不会被旧快照复活，预检可以据此立即 fail-closed。
type balanceExhaustionStore interface {
	MarkUserBalanceExhausted(ctx context.Context, userID int64) error
	ClearUserBalanceExhausted(ctx context.Context, userID int64) error
	IsUserBalanceExhausted(ctx context.Context, userID int64) (bool, error)
}

// billingReservationStore 是 BillingCache 的可选扩展能力：在转发上游之前，把本次请求的
// "最坏费用上界"原子地记入该用户的**在途预留**，请求结算完成后再归还。
//
// 为什么需要它：余额预检读到的余额是一个静态快照，它不包含"已经放行、但尚未结算"的请求
// 未来要花掉的钱。并发突发时 N 个请求读到同一份余额（即使刚刚用 DB 真值复核过），每个都
// 判定"余额够付自己这一笔"而全部放行；结算时只有前几笔扣得动，其余全部 actual_cost=0 ——
// 上游成本已经发生、钱却收不回来（"token 照统计、余额扣不动、请求不中断"的坏账来源）。
//
// 在途预留把这份"已承诺出去的额度"从可花余额里显式扣除，使准入判定变成原子操作：
// balance - 预留总额 >= reserve 的请求才放行，其余在预检即 403。该不变量在"结算扣钱"与
// "归还预留"以任意先后顺序发生时都保持成立。
//
// 未实现该能力的缓存（如测试用的轻量 stub）自动降级为 no-op，行为与修复前一致。
//
// scope 标识"同一份可被并发放大的额度"：余额模式用 userID，订阅模式用
// user+group（限额挂在两者之上）。两类额度互不干扰，因此各自独立计数。
type billingReservationStore interface {
	ReserveUserBalance(ctx context.Context, scope string, requestID string, amount float64, ttl time.Duration) (float64, error)
	ReleaseUserBalanceReservation(ctx context.Context, scope string, requestID string, amount float64, ttl time.Duration) error
}

type atomicBillingReservationStore interface {
	TryReserveUserBalance(ctx context.Context, scope string, requestID string, amount, maxTotal float64, ttl time.Duration) (float64, bool, error)
}

// billingReservationRenewer 是预留的**可选**能力：为长请求续期预留。
//
// 没有它，在线时间超过 billingReservationTTL 的请求（长流式响应完全可能跑到十分钟
// 以上）会在结算之前就丢掉护栏保护。未实现时心跳静默降级为 no-op —— 只降低保护
// 时长，不会产生错误结果。
type billingReservationRenewer interface {
	RenewUserBalanceReservation(ctx context.Context, scope string, requestID string, ttl time.Duration) error
}

// balanceReservationScope 余额模式的预留 scope：直接按用户聚合
// （余额是用户级资源，同一用户的所有余额模式请求共享同一份额度）。
func balanceReservationScope(userID int64) string {
	return strconv.FormatInt(userID, 10)
}

// subscriptionReservationScope 订阅模式的预留 scope。
//
// 限额配置在 Group 上（daily/weekly/monthly limit USD），作用于"用户 × 分组"，
// 因此 scope 必须带上 groupID：不同分组的限额是各自独立的额度，混在一起计数会
// 造成跨分组的误拦。
func subscriptionReservationScope(userID, groupID int64) string {
	return "sub:" + strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(groupID, 10)
}

// userPlatformQuotaReservationScope 是 user×platform 配额预留的 scope。
// 与余额 scope（纯 userID）分离：同一用户在不同平台上的配额是独立额度，
// 且与钱包余额是两套资源，不能混在同一个聚合键里。
func userPlatformQuotaReservationScope(userID int64, platform string) string {
	return "upq:" + strconv.FormatInt(userID, 10) + ":" + platform
}

// apiKeyQuotaReservationScope 是 API Key 总额度（api_key.quota）预留的 scope。
// 额度挂在单个 key 上，与用户余额、订阅限额都是独立资源，必须各自聚合：
// 同一个 key 的额度不能被钱包预留抵扣，反之亦然。
func apiKeyQuotaReservationScope(apiKeyID int64) string {
	return "apikey:" + strconv.FormatInt(apiKeyID, 10)
}

// APIKeyQuotaUsedLedger 是 API Key 已用额度（quota_used）的共享高水位账本。
//
// 它只有一个职责：把"结算提交后的 DB 真值"发布到 Redis，使准入在鉴权快照
// （L1 15s / L2 300s）过期之前也能看到最新已用额度。见
// reserveAPIKeyQuotaSpend 的说明与 repository 侧 apiKeyQuotaUsedLedgerKeyPrefix 注释。
//
// 之所以做成"注入的窄接口"而不是给 BillingCacheService 加构造参数：该服务有大量
// 调用点（含大量单测装配），沿用包内既有的包级 setter 注入约定
// （见 SetInflightReservationBudgetResolver / SetCodexCanonicalUserAgentResolver）。
// 未注入（降级/单测装配）时账本静默 no-op，准入退回"快照 + 在途预留"，行为与修复前一致。
type APIKeyQuotaUsedLedger interface {
	// GetAPIKeyQuotaUsedLedger 读取高水位；键不存在（含已过期）返回 0。
	GetAPIKeyQuotaUsedLedger(ctx context.Context, apiKeyID int64) (float64, error)
	// SetAPIKeyQuotaUsedLedger 以"只增不减"的方式发布结算后的 quota_used。
	SetAPIKeyQuotaUsedLedger(ctx context.Context, apiKeyID int64, quotaUsed float64) error
	// ClearAPIKeyQuotaUsedLedger 清除账本（管理员重置 quota_used 时必须调用，
	// 否则残留的高水位会在 TTL 内误拦该 key）。
	ClearAPIKeyQuotaUsedLedger(ctx context.Context, apiKeyID int64) error
}

var apiKeyQuotaUsedLedgerStore APIKeyQuotaUsedLedger

// SetAPIKeyQuotaUsedLedger 注入共享账本（nil = 关闭该护栏，退回修复前语义）。
func SetAPIKeyQuotaUsedLedger(store APIKeyQuotaUsedLedger) {
	apiKeyQuotaUsedLedgerStore = store
}

// APIKeyQuotaUsedLedgerReader 返回已注入的账本（nil 安全，供测试与 ops 使用）。
func APIKeyQuotaUsedLedgerReader() APIKeyQuotaUsedLedger {
	return apiKeyQuotaUsedLedgerStore
}

// billingReservationTTL 是在途预留的自愈 TTL：结算任务被丢弃 / 进程崩溃导致归还丢失时，
// 预留最多存活这么久，不会把余额永久钉死。
//
// 它是"自愈半径"：TTL 越长，坏账窗口的兜底越晚；TTL 越短，长请求越依赖心跳续期。
// 10 分钟配合心跳（billingReservationTTL/3 续期一次）可以覆盖任意时长的请求。
const billingReservationTTL = 10 * time.Minute

// billingReservationHeartbeatInterval 是长请求预留续期的间隔。取 TTL 的 1/3，
// 保证在网络抖动或单次续期失败后仍有两次重试余量。
//
// 声明为变量（而非常量）仅为让单测能把它压缩到毫秒级，生产路径不修改它。
var billingReservationHeartbeatInterval = billingReservationTTL / 3

// billingReservationReleaseTimeout 是归还预留的超时。请求收尾时请求 ctx 往往已经取消，
// 归还一律走脱离取消的独立 ctx（见 BillingReservationSlot.release）。
const billingReservationReleaseTimeout = 3 * time.Second

// newBillingReservationRequestID 生成一次请求的预留凭据 ID。
//
// 必须是每笔请求唯一且不含 ':' 的字符串（仓库层用 ':' 拼接用户与凭据），
// 去掉 UUID 的连字符即可同时满足两点。
func newBillingReservationRequestID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

// BillingReservationSlot 是一次请求持有的"在途预留"句柄。
//
// 生命周期：
//  1. CheckBillingEligibility 通过 WithBalanceReservation 收到槽位，放行时 bind 预留金额
//     与本次请求的凭据 ID，并启动心跳续期；
//  2. 请求收尾时按路径二选一：
//     - 提交了结算（usage 记录）任务：先 HandOff()，由结算任务在扣费完成后调用 Release()；
//     - 没有结算（错误提前返回等）：handler 的 defer 调 ReleaseOnExit() 立即归还。
//
// 为什么结算路径必须等扣费完成再归还：在扣费落地之前归还预留，等于把这份额度提前放给下一个
// 请求；等扣费真正发生时，那个请求的余额覆盖已经不成立——坏账窗口原样复现。结算任务被丢弃
// （显式 drop 策略 / 进程崩溃）时由 billingReservationTTL 兜底自愈；若丢弃动作是**已知**的
// （worker 池按 drop 语义拒绝入队），调用方应立即 Release 而不是等 TTL。
//
// 所有方法对 nil 接收者安全；Release / ReleaseOnExit 幂等，重复调用只归还一次。
type BillingReservationSlot struct {
	mu       sync.Mutex
	bindings []billingReservationBinding
	state    billingReservationState
	// heartbeat 非 nil 表示续期 goroutine 已启动；release 时 close 并置 nil。
	heartbeat chan struct{}
}

// billingReservationBinding 是槽位里的一条预留凭据。一次请求可能同时在多个
// scope 上占额度（余额模式下是用户余额 + 用户×平台配额），因此槽位按凭据
// 多绑定：归还与续期都以"每条凭据"为最小单位，避免漏还其中一条。
type billingReservationBinding struct {
	store     billingReservationStore
	scope     string
	requestID string
	amount    float64
}

// billingReservationState 描述槽位的归还状态机：idle → held →(settling)→ released。
type billingReservationState int

const (
	// billingReservationIdle：未绑定（本次请求没有产生预留，或预留能力不可用）。
	billingReservationIdle billingReservationState = iota
	// billingReservationHeld：已绑定预留金额，等待归还。
	billingReservationHeld
	// billingReservationSettling：归还责任已移交结算任务，handler 收尾不再归还。
	billingReservationSettling
	// billingReservationReleased：已归还（终态，幂等保护）。
	billingReservationReleased
)

// bind 绑定本次预留（service 内部使用）。state 允许 idle→held（首条绑定）与
// held→held（同一请求追加第二个 scope），禁止在 settling/released 上再绑定。
func (s *BillingReservationSlot) bind(store billingReservationStore, scope string, requestID string, amount float64) {
	if s == nil || store == nil || amount <= 0 || scope == "" || requestID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// requestID 是这条预留凭据的匹配键：归还与续期都以它为准，缺了它就无法
	// 区分"这笔归还到底是谁的"，晚到的归还会吃掉同 scope 下别人的预留。
	if s.state != billingReservationIdle && s.state != billingReservationHeld {
		return
	}
	s.bindings = append(s.bindings, billingReservationBinding{
		store:     store,
		scope:     scope,
		requestID: requestID,
		amount:    amount,
	})
	s.state = billingReservationHeld
}

// startHeartbeat 启动预留续期 goroutine（长请求保护）。
//
// 只有底层缓存实现了续期能力时才会真正启动；否则静默 no-op，行为与修复前一致。
// 幂等：已启动过（heartbeat != nil）时不再启动第二条 goroutine。
// 凭据过期或 Redis 故障会终止心跳（不再重试），归还责任仍由 Release/ReleaseOnExit 兜底。
func (s *BillingReservationSlot) startHeartbeat() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.state != billingReservationHeld || len(s.bindings) == 0 || s.heartbeat != nil {
		s.mu.Unlock()
		return
	}
	renewable := false
	for i := range s.bindings {
		if _, ok := s.bindings[i].store.(billingReservationRenewer); ok {
			renewable = true
			break
		}
	}
	if !renewable {
		s.mu.Unlock()
		return
	}
	done := make(chan struct{})
	s.heartbeat = done
	s.mu.Unlock()

	go func() {
		ticker := time.NewTicker(billingReservationHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if !s.renewBindings() {
					return
				}
			}
		}
	}()
}

// renewBindings 续期槽位里所有可续期的凭据。返回 false 表示心跳应停止：
// 没有任何凭据续期成功（全部过期），或出现非过期错误（沿用单绑定时代的保守语义）。
func (s *BillingReservationSlot) renewBindings() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	bindings := append([]billingReservationBinding(nil), s.bindings...)
	s.mu.Unlock()
	if len(bindings) == 0 {
		return false
	}
	alive := false
	for _, binding := range bindings {
		renewer, ok := binding.store.(billingReservationRenewer)
		if !ok {
			continue
		}
		renewCtx, cancel := context.WithTimeout(context.Background(), billingReservationReleaseTimeout)
		err := renewer.RenewUserBalanceReservation(renewCtx, binding.scope, binding.requestID, billingReservationTTL)
		cancel()
		if err == nil {
			RecordBillingReservationRenew()
			alive = true
			continue
		}
		if errors.Is(err, ErrBillingReservationExpired) {
			// 凭据已被 TTL 自愈回收（结算任务丢失 / 归还漏掉）：该条停止续期即可。
			RecordBillingReservationExpiredRelease()
			continue
		}
		RecordBillingReservationRenewError()
		logger.LegacyPrintf("service.billing_cache",
			"ALERT: renew in-flight balance reservation failed for scope %s: %v", binding.scope, err)
		return false
	}
	return alive
}

// HandOff 声明"本请求的归还责任移交给结算任务"：handler 收尾的 ReleaseOnExit 不再归还，
// 由结算任务在 RecordUsage（扣费）完成后调用 Release。
func (s *BillingReservationSlot) HandOff() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == billingReservationHeld {
		s.state = billingReservationSettling
	}
}

// Release 立即归还预留（幂等；未绑定 / 已归还时 no-op）。结算任务在扣费完成后调用。
//
// 结算任务被 worker 池丢弃时，调用方应立即调用它而不是等 TTL：那笔请求永远不会扣费，
// 预留也就没有任何理由继续占用用户额度。
func (s *BillingReservationSlot) Release(ctx context.Context) {
	s.release(ctx, false)
}

// Settle 是结算任务的收尾：结算成功（或业务性拒绝，如余额不足——此时钱包已打耗尽标记）
// 立即归还；结算因瞬时故障失败、本笔未扣费时**不归还**，让预留随 TTL 过期。
//
// 原因：未扣费的那笔上游成本仍挂在用户名下（待对账补扣），立即归还会把这份额度
// 马上放给下一笔请求，把一笔坏账放大成一串（审计 M3）。TTL 内额度被多占一份，
// 方向保守，且有上限。
func (s *BillingReservationSlot) Settle(ctx context.Context, settleErr error) {
	if s == nil {
		return
	}
	if settleErr != nil && usageBillingApplyRetryable(settleErr) {
		s.mu.Lock()
		heartbeat := s.heartbeat
		s.heartbeat = nil
		s.bindings = nil
		s.state = billingReservationReleased
		s.mu.Unlock()
		// 停心跳，否则续期会让这笔预留永不过期。
		if heartbeat != nil {
			close(heartbeat)
		}
		RecordBillingReservationHeldAfterSettlementFailure()
		return
	}
	s.Release(ctx)
}

// BillingReservationReceipt 是一条可序列化的预留凭据：异步任务（视频生成）的结算发生在
// 后续轮询请求里（可能落在别的实例），创建请求用 Detach 把预留交给持久化的待结算记录，
// 轮询请求结算时用 AdoptBillingReservation 接回并在扣费后归还。
type BillingReservationReceipt struct {
	Scope     string  `json:"scope"`
	RequestID string  `json:"request_id"`
	Amount    float64 `json:"amount"`
}

// detachedBillingReservationTTL 异步任务预留的存活上限：任务完成后的首次轮询会结算并归还；
// 客户端一直不轮询时，预留最多占用这么久后自愈。
const detachedBillingReservationTTL = time.Hour

// Detach 停止心跳、把预留续期到 detachedBillingReservationTTL，并把凭据交给调用方持久化
// （见 BillingReservationReceipt）。之后 handler 收尾的 ReleaseOnExit 为 no-op。
// 续期失败的凭据仍随原 TTL 自愈，照常返回（方向保守：额度只会被多占，不会少占）。
func (s *BillingReservationSlot) Detach(ctx context.Context) []BillingReservationReceipt {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.state != billingReservationHeld {
		s.mu.Unlock()
		return nil
	}
	bindings := s.bindings
	heartbeat := s.heartbeat
	s.bindings = nil
	s.heartbeat = nil
	s.state = billingReservationReleased
	s.mu.Unlock()
	if heartbeat != nil {
		close(heartbeat)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	receipts := make([]BillingReservationReceipt, 0, len(bindings))
	for _, binding := range bindings {
		if renewer, ok := binding.store.(billingReservationRenewer); ok {
			renewCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), billingReservationReleaseTimeout)
			err := renewer.RenewUserBalanceReservation(renewCtx, binding.scope, binding.requestID, detachedBillingReservationTTL)
			cancel()
			if errors.Is(err, ErrBillingReservationExpired) {
				continue
			}
			if err != nil {
				RecordBillingReservationRenewError()
			}
		}
		receipts = append(receipts, BillingReservationReceipt{Scope: binding.scope, RequestID: binding.requestID, Amount: binding.amount})
	}
	return receipts
}

// AdoptBillingReservation 把持久化的凭据接回 slot（不重新预留、不启动心跳），
// 之后照常由结算任务 Settle/Release 归还。slot 已持有预留时追加绑定。
func (s *BillingCacheService) AdoptBillingReservation(slot *BillingReservationSlot, receipts []BillingReservationReceipt) {
	if s == nil || slot == nil || len(receipts) == 0 {
		return
	}
	store, ok := s.balanceReservation()
	if !ok {
		return
	}
	for _, receipt := range receipts {
		slot.bind(store, receipt.Scope, receipt.RequestID, receipt.Amount)
	}
}

// ReleaseBillingReservationReceipts 直接归还持久化的凭据（任务失败 / 取消、不会再结算时）。
func (s *BillingCacheService) ReleaseBillingReservationReceipts(ctx context.Context, receipts []BillingReservationReceipt) {
	var slot BillingReservationSlot
	s.AdoptBillingReservation(&slot, receipts)
	slot.Release(ctx)
}

// ReleaseOnExit 请求收尾兜底：只有当预留没有移交给结算任务时才立即归还，
// 避免"结算还没扣钱、预留先还回去"的超额放行窗口。
func (s *BillingReservationSlot) ReleaseOnExit(ctx context.Context) {
	s.release(ctx, true)
}

// ResetForRetry allows a handler to reuse the slot after a route switch. The old
// reservation must be released first; settling slots are never reset.
func (s *BillingReservationSlot) ResetForRetry() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == billingReservationReleased {
		s.state = billingReservationIdle
	}
}

func (s *BillingReservationSlot) release(ctx context.Context, exitOnly bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	switch s.state {
	case billingReservationHeld:
	case billingReservationSettling:
		if exitOnly {
			s.mu.Unlock()
			return // 已移交结算任务：收尾兜底不再归还，等结算完成
		}
	default:
		s.mu.Unlock()
		return // idle / released：无事可做
	}
	bindings := s.bindings
	heartbeat := s.heartbeat
	s.bindings = nil
	s.heartbeat = nil
	s.state = billingReservationReleased
	s.mu.Unlock()

	// 先停心跳：否则续期可能在本笔归还之后把凭据"复活"，让额度一直挂着。
	if heartbeat != nil {
		close(heartbeat)
	}

	if len(bindings) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// 请求收尾时请求 ctx 通常已随连接取消；归还必须走脱离取消的独立 ctx，否则
	// Redis 写入会静默失败，把余额一直钉到 TTL 到期。
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), billingReservationReleaseTimeout)
	defer cancel()
	for _, binding := range bindings {
		if binding.store == nil || binding.amount <= 0 || binding.scope == "" || binding.requestID == "" {
			continue
		}
		if err := binding.store.ReleaseUserBalanceReservation(releaseCtx, binding.scope, binding.requestID, binding.amount, billingReservationTTL); err != nil {
			if errors.Is(err, ErrBillingReservationExpired) {
				// 凭据已随 TTL 过期：这块额度已由自愈回收，本次归还被安全忽略。
				RecordBillingReservationExpiredRelease()
				continue
			}
			RecordBillingReservationReleaseError()
			logger.LegacyPrintf("service.billing_cache", "ALERT: release in-flight balance reservation failed for scope %s: %v", binding.scope, err)
			continue
		}
		RecordBillingReservationReleased()
	}
}

type subscriptionCacheInvalidationPubSub interface {
	PublishSubscriptionCacheInvalidation(ctx context.Context, cacheKey string) error
	SubscribeSubscriptionCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error
}

// BillingCacheService 计费缓存服务
// 负责余额和订阅数据的缓存管理，提供高性能的计费资格检查
type BillingCacheService struct {
	cache                 BillingCache
	userRepo              UserRepository
	subRepo               UserSubscriptionRepository
	apiKeyRateLimitLoader apiKeyRateLimitLoader
	userRPMCache          UserRPMCache
	userGroupRateRepo     UserGroupRateRepository
	cfg                   *config.Config
	circuitBreaker        *billingCircuitBreaker
	userPlatformQuotaRepo UserPlatformQuotaRepository

	cacheWriteChan     chan cacheWriteTask
	cacheWriteWg       sync.WaitGroup
	cacheWriteStopOnce sync.Once
	cacheWriteMu       sync.RWMutex
	stopped            atomic.Bool
	balanceLoadSF      singleflight.Group
	quotaLoadSF        singleflight.Group
	balanceRecheckSF   singleflight.Group
	// 丢弃日志节流计数器（减少高负载下日志噪音）
	cacheWriteDropFullCount     uint64
	cacheWriteDropFullLastLog   int64
	cacheWriteDropClosedCount   uint64
	cacheWriteDropClosedLastLog int64
}

// NewBillingCacheService 创建计费缓存服务
func NewBillingCacheService(
	cache BillingCache,
	userRepo UserRepository,
	subRepo UserSubscriptionRepository,
	apiKeyRepo APIKeyRepository,
	userRPMCache UserRPMCache,
	userGroupRateRepo UserGroupRateRepository,
	cfg *config.Config,
	userPlatformQuotaRepo UserPlatformQuotaRepository,
) *BillingCacheService {
	svc := &BillingCacheService{
		cache:                 cache,
		userRepo:              userRepo,
		subRepo:               subRepo,
		apiKeyRateLimitLoader: apiKeyRepo,
		userRPMCache:          userRPMCache,
		userGroupRateRepo:     userGroupRateRepo,
		cfg:                   cfg,
		userPlatformQuotaRepo: userPlatformQuotaRepo,
	}
	svc.circuitBreaker = newBillingCircuitBreaker(cfg.Billing.CircuitBreaker)
	svc.startCacheWriteWorkers()
	return svc
}

// Stop 关闭缓存写入工作池
func (s *BillingCacheService) Stop() {
	s.cacheWriteStopOnce.Do(func() {
		s.stopped.Store(true)

		s.cacheWriteMu.Lock()
		ch := s.cacheWriteChan
		if ch != nil {
			close(ch)
		}
		s.cacheWriteMu.Unlock()

		if ch == nil {
			return
		}
		s.cacheWriteWg.Wait()

		s.cacheWriteMu.Lock()
		if s.cacheWriteChan == ch {
			s.cacheWriteChan = nil
		}
		s.cacheWriteMu.Unlock()
	})
}

func (s *BillingCacheService) startCacheWriteWorkers() {
	ch := make(chan cacheWriteTask, cacheWriteBufferSize)
	s.cacheWriteChan = ch
	for i := 0; i < cacheWriteWorkerCount; i++ {
		s.cacheWriteWg.Add(1)
		go s.cacheWriteWorker(ch)
	}
}

// enqueueCacheWrite 尝试将任务入队，队列满时返回 false（并记录告警）。
func (s *BillingCacheService) enqueueCacheWrite(task cacheWriteTask) (enqueued bool) {
	if s.stopped.Load() {
		s.logCacheWriteDrop(task, "closed")
		return false
	}

	s.cacheWriteMu.RLock()
	defer s.cacheWriteMu.RUnlock()

	if s.cacheWriteChan == nil {
		s.logCacheWriteDrop(task, "closed")
		return false
	}

	select {
	case s.cacheWriteChan <- task:
		return true
	default:
		// 队列满时不阻塞主流程，交由调用方决定是否同步回退。
		s.logCacheWriteDrop(task, "full")
		return false
	}
}

func (s *BillingCacheService) cacheWriteWorker(ch <-chan cacheWriteTask) {
	defer s.cacheWriteWg.Done()
	for task := range ch {
		ctx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
		switch task.kind {
		case cacheWriteSetBalance:
			s.setBalanceCacheWithGeneration(ctx, task.userID, task.balance, task.balanceGeneration)
		case cacheWriteSetSubscription:
			s.setSubscriptionCacheWithGeneration(ctx, task.userID, task.groupID, task.subscriptionData, task.subscriptionGeneration)
		case cacheWriteUpdateSubscriptionUsage:
			if s.cache != nil {
				if err := s.cache.UpdateSubscriptionUsage(ctx, task.userID, task.groupID, task.amount); err != nil {
					logger.LegacyPrintf("service.billing_cache", "Warning: update subscription cache failed for user %d group %d: %v", task.userID, task.groupID, err)
				}
			}
		case cacheWriteDeductBalance:
			if s.cache != nil {
				if err := s.cache.DeductUserBalance(ctx, task.userID, task.amount); err != nil {
					logger.LegacyPrintf("service.billing_cache", "Warning: deduct balance cache failed for user %d: %v", task.userID, err)
				}
			}
		case cacheWriteUpdateRateLimitUsage:
			if s.cache != nil {
				if err := s.cache.UpdateAPIKeyRateLimitUsage(ctx, task.apiKeyID, task.amount); err != nil {
					logger.LegacyPrintf("service.billing_cache", "Warning: update rate limit usage cache failed for api key %d: %v", task.apiKeyID, err)
				}
			}
		}
		cancel()
	}
}

// cacheWriteKindName 用于日志中的任务类型标识，便于排查丢弃原因。
func cacheWriteKindName(kind cacheWriteKind) string {
	switch kind {
	case cacheWriteSetBalance:
		return "set_balance"
	case cacheWriteSetSubscription:
		return "set_subscription"
	case cacheWriteUpdateSubscriptionUsage:
		return "update_subscription_usage"
	case cacheWriteDeductBalance:
		return "deduct_balance"
	case cacheWriteUpdateRateLimitUsage:
		return "update_rate_limit_usage"
	default:
		return "unknown"
	}
}

// logCacheWriteDrop 使用节流方式记录丢弃情况，并汇总丢弃数量。
func (s *BillingCacheService) logCacheWriteDrop(task cacheWriteTask, reason string) {
	var (
		countPtr *uint64
		lastPtr  *int64
	)
	switch reason {
	case "full":
		countPtr = &s.cacheWriteDropFullCount
		lastPtr = &s.cacheWriteDropFullLastLog
	case "closed":
		countPtr = &s.cacheWriteDropClosedCount
		lastPtr = &s.cacheWriteDropClosedLastLog
	default:
		return
	}

	atomic.AddUint64(countPtr, 1)
	now := time.Now().UnixNano()
	last := atomic.LoadInt64(lastPtr)
	if now-last < int64(cacheWriteDropLogInterval) {
		return
	}
	if !atomic.CompareAndSwapInt64(lastPtr, last, now) {
		return
	}
	dropped := atomic.SwapUint64(countPtr, 0)
	if dropped == 0 {
		return
	}
	logger.LegacyPrintf("service.billing_cache", "Warning: cache write queue %s, dropped %d tasks in last %s (latest kind=%s user %d group %d)",
		reason,
		dropped,
		cacheWriteDropLogInterval,
		cacheWriteKindName(task.kind),
		task.userID,
		task.groupID,
	)
}

// ============================================
// 余额缓存方法
// ============================================

// GetUserBalance 获取用户余额（优先从缓存读取）
func (s *BillingCacheService) GetUserBalance(ctx context.Context, userID int64) (float64, error) {
	if s.cache == nil {
		// Redis不可用，直接查询数据库
		return s.getUserBalanceFromDB(ctx, userID)
	}

	// 尝试从缓存读取
	balance, err := s.cache.GetUserBalance(ctx, userID)
	if err == nil {
		return balance, nil
	}

	// 缓存未命中：singleflight 合并同一 userID 的并发回源请求。
	value, err, _ := s.balanceLoadSF.Do(strconv.FormatInt(userID, 10), func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.Background(), balanceLoadTimeout)
		defer cancel()

		// 变动代号必须在 DB 读取**之前**取：晚于读取取到的代号会把"读取期间发生的
		// 扣费"一起掩盖掉，条件写回就失去意义（审计 R9）。
		generation := s.balanceGenerationFor(loadCtx, userID)

		balance, err := s.getUserBalanceFromDB(loadCtx, userID)
		if err != nil {
			return nil, err
		}

		// 异步建立缓存（条件写回：期间发生过余额变动就丢弃这份快照）
		_ = s.enqueueCacheWrite(cacheWriteTask{
			kind:              cacheWriteSetBalance,
			userID:            userID,
			balance:           balance,
			balanceGeneration: generation,
		})
		return balance, nil
	})
	if err != nil {
		return 0, err
	}
	balance, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("unexpected balance type: %T", value)
	}
	return balance, nil
}

// getUserBalanceFromDB 从数据库获取用户余额
func (s *BillingCacheService) getUserBalanceFromDB(ctx context.Context, userID int64) (float64, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("get user balance: %w", err)
	}
	return user.Balance, nil
}

// recheckUserBalanceFromDB 读取 DB 真值用于"贴近底线"的预检复核。
//
// 余额贴近底线时每笔请求都要复核，并发突发（同一用户的 N 笔请求同时到达）
// 会让 N 次等价的 PK 查询同时涌向连接池：连接池被瞬时打满后，后续复核拿不
// 到 DB 连接（pq: sorry, too many clients already），fail-closed 变成大范围 503。
// 这里用 singleflight 把同一用户的并发复核合并为一次回源，等待者共享同一结果；
// 复核跑在独立超时的后台上下文上，不受单个请求取消影响（等待上限即该超时）。
func (s *BillingCacheService) recheckUserBalanceFromDB(userID int64) (float64, error) {
	value, err, _ := s.balanceRecheckSF.Do(strconv.FormatInt(userID, 10), func() (any, error) {
		recheckCtx, cancel := context.WithTimeout(context.Background(), balanceRecheckTimeout)
		defer cancel()
		return s.getUserBalanceFromDB(recheckCtx, userID)
	})
	if err != nil {
		return 0, err
	}
	balance, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("get user balance: unexpected recheck result type %T", value)
	}
	return balance, nil
}

// setBalanceCacheWithGeneration 发布余额缓存快照。
//
// generation 非空且底层缓存支持条件写回时，只有"读取方发起 DB 读取之后没有发生过
// 余额变动"才真的发布；被拦下的写回不创建任何键，下一次读取回源真值自愈（审计 R9）。
func (s *BillingCacheService) setBalanceCacheWithGeneration(ctx context.Context, userID int64, balance float64, generation string) {
	if s.cache == nil {
		return
	}
	if guard, ok := s.cache.(balancePublishGuard); ok && generation != "" {
		published, err := guard.SetUserBalanceIfGeneration(ctx, userID, balance, generation)
		if err != nil {
			logger.LegacyPrintf("service.billing_cache", "Warning: conditional set balance cache failed for user %d: %v", userID, err)
			return
		}
		if !published {
			logger.LegacyPrintf("service.billing_cache",
				"balance cache snapshot discarded for user %d: balance changed after the DB read (审计 R9)", userID)
		}
		return
	}
	if err := s.cache.SetUserBalance(ctx, userID, balance); err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: set balance cache failed for user %d: %v", userID, err)
	}
}

// balanceGenerationFor 取一份"余额变动代号"，必须在**发起 DB 读取之前**调用。
//
// 能力不可用（轻量 stub / 旧实现）时返回空串，调用方退回无条件写回，与修复前一致。
func (s *BillingCacheService) balanceGenerationFor(ctx context.Context, userID int64) string {
	guard, ok := s.cache.(balancePublishGuard)
	if !ok {
		return ""
	}
	generation, err := guard.BalanceGeneration(ctx, userID)
	if err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: read balance generation for user %d failed: %v", userID, err)
		return ""
	}
	return generation
}

// DeductBalanceCache 扣减余额缓存（同步调用）
func (s *BillingCacheService) DeductBalanceCache(ctx context.Context, userID int64, amount float64) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.DeductUserBalance(ctx, userID, amount)
}

// SetUserBalanceCache 同步覆写用户余额缓存（扣费后以 DB 事务结果为准写回，
// 避免并发扣费下 Redis INCR 类操作产生负余额视图）。
//
// 这是**无条件**发布：只适用于"调用方手上的值就是刚提交的 DB 事务结果"的场景。
// 回源/复核得到的快照不要用它 —— 那份值可能早于读取期间发生的扣费，必须走
// setBalanceCacheWithGeneration 的条件写回（审计 R9）。
func (s *BillingCacheService) SetUserBalanceCache(ctx context.Context, userID int64, balance float64) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.SetUserBalance(ctx, userID, balance)
}

// QueueDeductBalance 异步扣减余额缓存
func (s *BillingCacheService) QueueDeductBalance(userID int64, amount float64) {
	if s.cache == nil {
		return
	}
	// 队列满时同步回退，避免关键扣减被静默丢弃。
	if s.enqueueCacheWrite(cacheWriteTask{
		kind:   cacheWriteDeductBalance,
		userID: userID,
		amount: amount,
	}) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
	defer cancel()
	if err := s.DeductBalanceCache(ctx, userID, amount); err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: deduct balance cache fallback failed for user %d: %v", userID, err)
	}
}

// InvalidateUserBalance 失效用户余额缓存
func (s *BillingCacheService) InvalidateUserBalance(ctx context.Context, userID int64) error {
	if s.cache == nil {
		return nil
	}
	if err := s.cache.InvalidateUserBalance(ctx, userID); err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: invalidate balance cache failed for user %d: %v", userID, err)
		return err
	}
	return nil
}

// ============================================
// 订阅缓存方法
// ============================================

// GetSubscriptionStatus 获取订阅状态（优先从缓存读取）
func (s *BillingCacheService) GetSubscriptionStatus(ctx context.Context, userID, groupID int64) (*subscriptionCacheData, error) {
	if s.cache == nil {
		return s.getSubscriptionFromDB(ctx, userID, groupID)
	}

	// 尝试从缓存读取
	cacheData, err := s.cache.GetSubscriptionCache(ctx, userID, groupID)
	if err == nil && cacheData != nil {
		return s.convertFromPortsData(cacheData), nil
	}

	// 缓存未命中，从数据库读取
	// 变动代号必须在 DB 读取**之前**取：晚于读取取到的代号会把"读取期间发生的用量
	// 变动"一起掩盖掉，条件写回就失去意义（与余额缓存 R9 同构）。
	generation := s.subscriptionGenerationFor(ctx, userID, groupID)
	data, err := s.getSubscriptionFromDB(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}

	// 异步建立缓存
	_ = s.enqueueCacheWrite(cacheWriteTask{
		kind:             cacheWriteSetSubscription,
		userID:           userID,
		groupID:          groupID,
		subscriptionData: data,
		// 条件写回：读取期间有变动就丢弃这份快照（否则迟到的旧快照会把结算后的
		// 真实 usage 覆盖回偏小值）。
		subscriptionGeneration: generation,
	})

	return data, nil
}

func (s *BillingCacheService) convertFromPortsData(data *SubscriptionCacheData) *subscriptionCacheData {
	return &subscriptionCacheData{
		Status:       data.Status,
		ExpiresAt:    data.ExpiresAt,
		DailyUsage:   data.DailyUsage,
		WeeklyUsage:  data.WeeklyUsage,
		MonthlyUsage: data.MonthlyUsage,
		Version:      data.Version,
	}
}

func (s *BillingCacheService) convertToPortsData(data *subscriptionCacheData) *SubscriptionCacheData {
	return &SubscriptionCacheData{
		Status:       data.Status,
		ExpiresAt:    data.ExpiresAt,
		DailyUsage:   data.DailyUsage,
		WeeklyUsage:  data.WeeklyUsage,
		MonthlyUsage: data.MonthlyUsage,
		Version:      data.Version,
	}
}

// getSubscriptionFromDB 从数据库获取订阅数据
func (s *BillingCacheService) getSubscriptionFromDB(ctx context.Context, userID, groupID int64) (*subscriptionCacheData, error) {
	sub, err := s.subRepo.GetActiveByUserIDAndGroupID(ctx, userID, groupID)
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}

	return &subscriptionCacheData{
		Status:       sub.Status,
		ExpiresAt:    sub.ExpiresAt,
		DailyUsage:   sub.DailyUsageUSD,
		WeeklyUsage:  sub.WeeklyUsageUSD,
		MonthlyUsage: sub.MonthlyUsageUSD,
		Version:      sub.UpdatedAt.Unix(),
	}, nil
}

// setSubscriptionCache 设置订阅缓存
func (s *BillingCacheService) setSubscriptionCache(ctx context.Context, userID, groupID int64, data *subscriptionCacheData) {
	if s.cache == nil || data == nil {
		return
	}
	if err := s.cache.SetSubscriptionCache(ctx, userID, groupID, s.convertToPortsData(data)); err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: set subscription cache failed for user %d group %d: %v", userID, groupID, err)
	}
}

// subscriptionPublishGuard 是订阅缓存回源写回的**可选**能力：支持"读到订阅之后没有
// 发生过用量变动才发布"的条件写回（与余额缓存的 balancePublishGuard 同构）。
//
// 未实现该能力的缓存（测试用的轻量 stub、旧实现）自动降级为无条件写回，
// 行为与修复前一致 —— 只降低防护强度，不会产生错误结果。
type subscriptionPublishGuard interface {
	// SubscriptionGeneration 返回当前的订阅缓存变动代号（键不存在时归一化为 "0"）。
	SubscriptionGeneration(ctx context.Context, userID, groupID int64) (string, error)
	// SetSubscriptionCacheIfGeneration 仅在代号未变时发布快照，返回是否发布成功。
	SetSubscriptionCacheIfGeneration(ctx context.Context, userID, groupID int64, data *SubscriptionCacheData, generation string) (bool, error)
}

// subscriptionUsageApplier 是 BillingCache 的**可选**扩展能力：报告订阅用量的同步
// 累加是否真的落到缓存（applied=false = 缓存键不存在，脚本静默跳过）。
// 做成可选接口是为了不破坏轻量 stub 的编译。
type subscriptionUsageApplier interface {
	UpdateSubscriptionUsageApplied(ctx context.Context, userID, groupID int64, cost float64) (bool, error)
}

// apiKeyRateLimitUsageApplier 同理，作用于 API Key 限流用量。
type apiKeyRateLimitUsageApplier interface {
	UpdateAPIKeyRateLimitUsageApplied(ctx context.Context, keyID int64, cost float64) (bool, error)
}

// setSubscriptionCacheWithGeneration 发布订阅缓存快照。
//
// generation 非空且底层缓存支持条件写回时，只有"读取方发起 DB 读取之后没有发生过
// 订阅用量变动"才真的发布；被拦下的写回不创建任何键，下一次读取回源真值自愈。
func (s *BillingCacheService) setSubscriptionCacheWithGeneration(ctx context.Context, userID, groupID int64, data *subscriptionCacheData, generation string) {
	if s.cache == nil || data == nil {
		return
	}
	if guard, ok := s.cache.(subscriptionPublishGuard); ok && generation != "" {
		published, err := guard.SetSubscriptionCacheIfGeneration(ctx, userID, groupID, s.convertToPortsData(data), generation)
		if err != nil {
			logger.LegacyPrintf("service.billing_cache", "Warning: conditional set subscription cache failed for user %d group %d: %v", userID, groupID, err)
			return
		}
		if !published {
			logger.LegacyPrintf("service.billing_cache",
				"subscription cache snapshot discarded for user %d group %d: usage changed after the DB read", userID, groupID)
		}
		return
	}
	s.setSubscriptionCache(ctx, userID, groupID, data)
}

// subscriptionGenerationFor 取一份"订阅缓存变动代号"，必须在**发起 DB 读取之前**调用。
//
// 能力不可用（轻量 stub / 旧实现）时返回空串，调用方退回无条件写回，与修复前一致。
func (s *BillingCacheService) subscriptionGenerationFor(ctx context.Context, userID, groupID int64) string {
	guard, ok := s.cache.(subscriptionPublishGuard)
	if !ok {
		return ""
	}
	generation, err := guard.SubscriptionGeneration(ctx, userID, groupID)
	if err != nil {
		logger.LegacyPrintf("service.billing_cache",
			"Warning: read subscription generation for user %d group %d failed: %v", userID, groupID, err)
		return ""
	}
	return generation
}

// UpdateSubscriptionUsage 更新订阅用量缓存（同步调用）
func (s *BillingCacheService) UpdateSubscriptionUsage(ctx context.Context, userID, groupID int64, costUSD float64) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.UpdateSubscriptionUsage(ctx, userID, groupID, costUSD)
}

// QueueUpdateSubscriptionUsage 异步更新订阅用量缓存
func (s *BillingCacheService) QueueUpdateSubscriptionUsage(userID, groupID int64, costUSD float64) {
	if s.cache == nil {
		return
	}
	// 队列满时同步回退，确保订阅用量及时更新。
	if s.enqueueCacheWrite(cacheWriteTask{
		kind:    cacheWriteUpdateSubscriptionUsage,
		userID:  userID,
		groupID: groupID,
		amount:  costUSD,
	}) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
	defer cancel()
	if err := s.UpdateSubscriptionUsage(ctx, userID, groupID, costUSD); err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: update subscription cache fallback failed for user %d group %d: %v", userID, groupID, err)
	}
}

// SyncSubscriptionUsageAfterSettlement 在结算提交后**同步**把订阅用量累加进缓存。
//
// 为什么不能用 QueueUpdateSubscriptionUsage（异步 worker）：
//   - 预检读的是 Redis 里的 usage 快照，护栏是 `usage + Σ预留 < 限额`；
//   - handler 的结算任务闭包在 RecordUsage 返回后 `defer Release()` 归还预留，
//     而异步 worker 可能还没把本次 usage 累加进缓存。
//   - 于是"预留已归零、usage 仍是旧值"同时成立，下一批并发请求会重新拿到
//     一整份剩余额度再次放行 —— 订阅限额被重复消耗（与 API Key 额度冻结快照同构）。
//
// 因此这里同步写（Redis HINCRBYFLOAT，通常 < 1ms），并且只在"累加被记录"之后
// 才让调用方继续归还预留；失败时按保守方向补救：
//  1. 计一次 ALERT + 失败计数（ops 面板可观测，不再静默吞掉）；
//  2. 失效订阅缓存，让下一次预检回源到 DB 真值（结算已经写进 DB），
//     即使缓存键缺失导致累加被跳过，用量视图也不会偏小。
func (s *BillingCacheService) SyncSubscriptionUsageAfterSettlement(ctx context.Context, userID, groupID int64, costUSD float64) {
	if s == nil || s.cache == nil || costUSD <= 0 {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheWriteTimeout)
	defer cancel()
	applied, err := s.applySubscriptionUsageSync(writeCtx, userID, groupID, costUSD)
	if err == nil && applied {
		return
	}
	if err != nil {
		RecordBillingSubscriptionUsageSyncError()
		logger.LegacyPrintf("service.billing_cache",
			"ALERT: sync subscription usage cache failed user=%d group=%d cost=%f: %v (invalidating cache, next preflight reads DB truth)",
			userID, groupID, costUSD, err)
	} else {
		// 累加被跳过（订阅缓存键不存在）：这笔用量只在 DB 里，缓存快照偏小。
		// 不删缓存的话，预检会在 TTL 内继续按偏小的用量放行。
		RecordBillingSubscriptionUsageSyncError()
		logger.LegacyPrintf("service.billing_cache",
			"ALERT: subscription usage cache increment skipped (missing key) user=%d group=%d cost=%f (invalidating cache, next preflight reads DB truth)",
			userID, groupID, costUSD)
	}
	if invalidateErr := s.InvalidateSubscription(writeCtx, userID, groupID); invalidateErr != nil {
		logger.LegacyPrintf("service.billing_cache",
			"ALERT: invalidate subscription cache after sync usage failure failed user=%d group=%d: %v",
			userID, groupID, invalidateErr)
	}
}

// applySubscriptionUsageSync 累加订阅用量并报告是否真的落到缓存。
//
// 底层实现支持"applied"回复（production 的 *billingCache）时用它；
// 轻量 stub / 旧实现按原语义只看错误（视作 applied=true）。
func (s *BillingCacheService) applySubscriptionUsageSync(ctx context.Context, userID, groupID int64, costUSD float64) (bool, error) {
	if applier, ok := s.cache.(subscriptionUsageApplier); ok {
		return applier.UpdateSubscriptionUsageApplied(ctx, userID, groupID, costUSD)
	}
	return true, s.cache.UpdateSubscriptionUsage(ctx, userID, groupID, costUSD)
}

// SyncAPIKeyRateLimitUsageAfterSettlement 在结算提交后同步累加 API Key 限流用量。
//
// 与订阅用量同理：限流预检（checkAPIKeyRateLimits）优先读 Redis 快照，
// 若累加还躺在异步队列里，下一批请求会按偏小的窗口用量放行（限流被穿透）。
// 同步写失败时失效限流缓存，让下一次预检回源到 DB 真值（结算已写进 DB）。
func (s *BillingCacheService) SyncAPIKeyRateLimitUsageAfterSettlement(ctx context.Context, apiKeyID int64, cost float64) {
	if s == nil || s.cache == nil || apiKeyID <= 0 || cost <= 0 {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheWriteTimeout)
	defer cancel()
	applied, err := s.applyAPIKeyRateLimitUsageSync(writeCtx, apiKeyID, cost)
	if err == nil && applied {
		return
	}
	if err != nil {
		RecordBillingAPIKeyRateLimitSyncError()
		logger.LegacyPrintf("service.billing_cache",
			"ALERT: sync api key rate limit usage cache failed key=%d cost=%f: %v (invalidating cache, next preflight reads DB truth)",
			apiKeyID, cost, err)
	} else {
		// 累加被跳过（限流缓存键不存在）：窗口用量只在 DB 里，缓存快照偏小。
		RecordBillingAPIKeyRateLimitSyncError()
		logger.LegacyPrintf("service.billing_cache",
			"ALERT: api key rate limit usage cache increment skipped (missing key) key=%d cost=%f (invalidating cache, next preflight reads DB truth)",
			apiKeyID, cost)
	}
	if invalidateErr := s.InvalidateAPIKeyRateLimit(writeCtx, apiKeyID); invalidateErr != nil {
		logger.LegacyPrintf("service.billing_cache",
			"ALERT: invalidate api key rate limit cache after sync usage failure failed key=%d: %v",
			apiKeyID, invalidateErr)
	}
}

// applyAPIKeyRateLimitUsageSync 累加限流用量并报告是否真的落到缓存（语义同
// applySubscriptionUsageSync）。
func (s *BillingCacheService) applyAPIKeyRateLimitUsageSync(ctx context.Context, apiKeyID int64, cost float64) (bool, error) {
	if applier, ok := s.cache.(apiKeyRateLimitUsageApplier); ok {
		return applier.UpdateAPIKeyRateLimitUsageApplied(ctx, apiKeyID, cost)
	}
	return true, s.cache.UpdateAPIKeyRateLimitUsage(ctx, apiKeyID, cost)
}

// InvalidateSubscription 失效指定订阅缓存
func (s *BillingCacheService) InvalidateSubscription(ctx context.Context, userID, groupID int64) error {
	if s.cache == nil {
		return nil
	}
	if err := s.cache.InvalidateSubscriptionCache(ctx, userID, groupID); err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: invalidate subscription cache failed for user %d group %d: %v", userID, groupID, err)
		return err
	}
	return nil
}

func (s *BillingCacheService) PublishSubscriptionCacheInvalidation(ctx context.Context, cacheKey string) error {
	if s.cache == nil {
		return nil
	}
	pubsub, ok := s.cache.(subscriptionCacheInvalidationPubSub)
	if !ok {
		return nil
	}
	return pubsub.PublishSubscriptionCacheInvalidation(ctx, cacheKey)
}

func (s *BillingCacheService) SubscribeSubscriptionCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	if s.cache == nil {
		return nil
	}
	pubsub, ok := s.cache.(subscriptionCacheInvalidationPubSub)
	if !ok {
		return nil
	}
	return pubsub.SubscribeSubscriptionCacheInvalidation(ctx, handler)
}

// InvalidateAPIKeyRateLimit invalidates the Redis rate-limit usage cache for an API key.
func (s *BillingCacheService) InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error {
	if s.cache == nil {
		return nil
	}
	if err := s.cache.InvalidateAPIKeyRateLimit(ctx, keyID); err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: invalidate api key rate limit cache failed for key %d: %v", keyID, err)
		return err
	}
	return nil
}

// ============================================
// API Key 限速缓存方法
// ============================================

// checkAPIKeyRateLimits checks rate limit windows for an API key.
// It loads usage from Redis cache (falling back to DB on cache miss),
// resets expired windows in-memory and triggers async DB reset,
// and returns an error if any window limit is exceeded.
func (s *BillingCacheService) checkAPIKeyRateLimits(ctx context.Context, apiKey *APIKey) error {
	if s.cache == nil {
		// No cache: fall back to reading from DB directly
		if s.apiKeyRateLimitLoader == nil {
			return nil
		}
		data, err := s.apiKeyRateLimitLoader.GetRateLimitData(ctx, apiKey.ID)
		if err != nil {
			return nil // Don't block requests on DB errors
		}
		return s.evaluateRateLimits(ctx, apiKey, data.Usage5h, data.Usage1d, data.Usage7d,
			data.Window5hStart, data.Window1dStart, data.Window7dStart)
	}

	cacheData, err := s.cache.GetAPIKeyRateLimit(ctx, apiKey.ID)
	if err != nil {
		// Cache miss: load from DB and populate cache
		if s.apiKeyRateLimitLoader == nil {
			return nil
		}
		dbData, dbErr := s.apiKeyRateLimitLoader.GetRateLimitData(ctx, apiKey.ID)
		if dbErr != nil {
			return nil // Don't block requests on DB errors
		}
		// Build cache entry from DB data
		cacheEntry := &APIKeyRateLimitCacheData{
			Usage5h: dbData.Usage5h,
			Usage1d: dbData.Usage1d,
			Usage7d: dbData.Usage7d,
		}
		if dbData.Window5hStart != nil {
			cacheEntry.Window5h = dbData.Window5hStart.Unix()
		}
		if dbData.Window1dStart != nil {
			cacheEntry.Window1d = dbData.Window1dStart.Unix()
		}
		if dbData.Window7dStart != nil {
			cacheEntry.Window7d = dbData.Window7dStart.Unix()
		}
		_ = s.cache.SetAPIKeyRateLimit(ctx, apiKey.ID, cacheEntry)
		cacheData = cacheEntry
	}

	var w5h, w1d, w7d *time.Time
	if cacheData.Window5h > 0 {
		t := time.Unix(cacheData.Window5h, 0)
		w5h = &t
	}
	if cacheData.Window1d > 0 {
		t := time.Unix(cacheData.Window1d, 0)
		w1d = &t
	}
	if cacheData.Window7d > 0 {
		t := time.Unix(cacheData.Window7d, 0)
		w7d = &t
	}
	return s.evaluateRateLimits(ctx, apiKey, cacheData.Usage5h, cacheData.Usage1d, cacheData.Usage7d, w5h, w1d, w7d)
}

// evaluateRateLimits checks usage against limits, triggering async resets for expired windows.
func (s *BillingCacheService) evaluateRateLimits(ctx context.Context, apiKey *APIKey, usage5h, usage1d, usage7d float64, w5h, w1d, w7d *time.Time) error {
	needsReset := false

	// Reset expired windows in-memory for check purposes
	if IsWindowExpired(w5h, RateLimitWindow5h) {
		usage5h = 0
		needsReset = true
	}
	if IsWindowExpired(w1d, RateLimitWindow1d) {
		usage1d = 0
		needsReset = true
	}
	if IsWindowExpired(w7d, RateLimitWindow7d) {
		usage7d = 0
		needsReset = true
	}

	// Trigger async DB reset if any window expired
	if needsReset {
		keyID := apiKey.ID
		go func() {
			resetCtx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
			defer cancel()
			if s.apiKeyRateLimitLoader != nil {
				// Use the repo directly - reset then reload cache
				if loader, ok := s.apiKeyRateLimitLoader.(interface {
					ResetRateLimitWindows(ctx context.Context, id int64) error
				}); ok {
					if err := loader.ResetRateLimitWindows(resetCtx, keyID); err != nil {
						logger.LegacyPrintf("service.billing_cache", "Warning: reset rate limit windows failed for api key %d: %v", keyID, err)
					}
				}
			}
			// Invalidate cache so next request loads fresh data
			if s.cache != nil {
				if err := s.cache.InvalidateAPIKeyRateLimit(resetCtx, keyID); err != nil {
					logger.LegacyPrintf("service.billing_cache", "Warning: invalidate rate limit cache failed for api key %d: %v", keyID, err)
				}
			}
		}()
	}

	// Check limits
	if apiKey.RateLimit5h > 0 && usage5h >= apiKey.RateLimit5h {
		return ErrAPIKeyRateLimit5hExceeded
	}
	if apiKey.RateLimit1d > 0 && usage1d >= apiKey.RateLimit1d {
		return ErrAPIKeyRateLimit1dExceeded
	}
	if apiKey.RateLimit7d > 0 && usage7d >= apiKey.RateLimit7d {
		return ErrAPIKeyRateLimit7dExceeded
	}
	return nil
}

// QueueUpdateAPIKeyRateLimitUsage asynchronously updates rate limit usage in the cache.
func (s *BillingCacheService) QueueUpdateAPIKeyRateLimitUsage(apiKeyID int64, cost float64) {
	if s.cache == nil {
		return
	}
	s.enqueueCacheWrite(cacheWriteTask{
		kind:     cacheWriteUpdateRateLimitUsage,
		apiKeyID: apiKeyID,
		amount:   cost,
	})
}

// userPlatformQuotaUsageApplier 是 BillingCache 的**可选**扩展能力：
// 报告本次累加是否真的落到缓存（applied=false = 被 key 缺失 / 旧 schema 守卫跳过）。
//
// 做成可选接口而不是往 BillingCache 里加方法：后者会破坏所有轻量 stub 的编译
// （测试与降级装配），而这里只影响 production 的 *billingCache 实现。
type userPlatformQuotaUsageApplier interface {
	IncrUserPlatformQuotaUsageCacheApplied(ctx context.Context, userID int64, platform string, cost float64, ttl time.Duration, markDirty bool) (bool, error)
}

// IncrementUserPlatformQuotaUsage 同步累加 user × platform usage 到 Redis 缓存。
//
// 设计：同步写入而非异步入队。同步写确保下次 preflight 立即看到最新 usage，
// 把 TOCTOU 超支窗口限制在并发 in-flight 请求数量内（而非随时间无限累积）。
// 写延迟通常 < 1ms（本地 Redis），换取 quota 视图实时性的取舍合理。
//
// 返回值 handledByDB=true 表示"这份增量已由本函数同步写进 DB"（缓存累加被跳过或
// Redis 写失败），调用方不要再重复写一遍 —— 否则会双记。false 表示缓存累加成功，
// DB 持久化仍按原路径（flusher 或调用方直写）处理。
//
// 为什么需要 DB 兜底：累加脚本在 key 不存在 / entry 为旧 schema 时静默 return 0，
// 此前只看 error 的调用方完全看不到"增量被丢弃"。这份丢失在 flusher 模式下尤其危险：
// 那笔 cost 连 DB 都不会有，usage 永久偏小 = 平台配额可被重复放行。
func (s *BillingCacheService) IncrementUserPlatformQuotaUsage(userID int64, platform string, cost float64) (handledByDB bool) {
	if s.cache == nil {
		return false
	}
	if platform == "" || cost <= 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
	defer cancel()
	ttl := time.Duration(s.cfg.Billing.UserPlatformQuotaCacheTTLSeconds) * time.Second
	markDirty := s.cfg.Database.UserPlatformQuotaFlusherEnabled

	var (
		applied     bool
		incrErr     error
		supportsAck bool
	)
	if applier, ok := s.cache.(userPlatformQuotaUsageApplier); ok {
		supportsAck = true
		applied, incrErr = applier.IncrUserPlatformQuotaUsageCacheApplied(ctx, userID, platform, cost, ttl, markDirty)
	} else {
		// 轻量 stub / 旧实现：拿不到"是否真的累加"的回复，按原语义只处理错误。
		applied, incrErr = true, s.cache.IncrUserPlatformQuotaUsageCache(ctx, userID, platform, cost, ttl, markDirty)
	}
	if incrErr != nil {
		logger.LegacyPrintf("service.billing_cache",
			"ALERT: incr user platform quota cache failed user=%d platform=%s cost=%f: %v (falling back to synchronous DB increment)",
			userID, platform, cost, incrErr)
	}
	if incrErr == nil && applied {
		return false
	}

	// 走到这里有两种情况，都必须补救（方向：宁可 DB usage 偏保守，也不能偏小）：
	//  1. 累加被守卫跳过（key 缺失 / 旧 schema）：增量已丢，缓存里没有它；
	//  2. Redis 写失败：增量既不在缓存也不在 DB。
	// 处置：删缓存（让下次 preflight 从 DB 重载，而不是继续用少了这笔的旧快照）
	//       + 同步写 DB（把这笔 cost 持久化，成为新的真值来源）。
	if err := s.cache.DeleteUserPlatformQuotaCache(ctx, userID, platform); err != nil {
		logger.LegacyPrintf("service.billing_cache",
			"ALERT: delete user platform quota cache after skipped/failed incr failed user=%d platform=%s: %v",
			userID, platform, err)
	}
	if !supportsAck {
		// 旧实现错误路径：保持原有语义，由调用方按 flusher 配置决定是否直写 DB。
		return false
	}
	if s.userPlatformQuotaRepo == nil {
		logger.LegacyPrintf("service.billing_cache",
			"ALERT: user platform quota increment lost user=%d platform=%s cost=%f (no DB repo to fall back to)",
			userID, platform, cost)
		return false
	}
	if err := s.userPlatformQuotaRepo.IncrementUsageWithReset(ctx, userID, platform, cost, time.Now().UTC()); err != nil {
		logger.LegacyPrintf("service.billing_cache",
			"ALERT: synchronous DB fallback for lost user platform quota increment failed user=%d platform=%s cost=%f: %v (usage will be under-counted until reconciled)",
			userID, platform, cost, err)
		return false
	}
	return true
}

// ============================================
// 统一检查方法
// ============================================

// BillingEligibilityOption 为 CheckBillingEligibility 增加可选的附加闸门。
type BillingEligibilityOption func(*billingEligibilityOptions)

// billingEligibilityOptions 预检附加参数的收口结构。
type billingEligibilityOptions struct {
	// maxRequestSpend 是本次请求的"最坏费用上界"（USD，由 EstimateRequestSpendUpperBound 给出）。
	// >0 时余额模式预检要求余额能覆盖「封底线 + maxRequestSpend」，否则直接 403：
	// 付不满最坏费用的请求绝不转发到上游（上游成本一旦发生就无法追回）。
	maxRequestSpend float64
	// reservationSlot 非 nil 时，预检放行前会把本次最坏费用原子地记入用户的"在途预留"，
	// 使并发请求不能再用同一份余额快照同时放行（结算才发现的坏账来源）。
	reservationSlot *BillingReservationSlot
	// skipRPM 跳过 RPM 计数：同一连接内的后续 turn 只需额度/预留闸门，
	// RPM 已在握手准入时计过一次，重复计数会改变既有限流语义。
	skipRPM bool
}

// WithoutRPMCheck 跳过本次预检的 RPM 计数（仅用于同一请求/连接内的复检）。
func WithoutRPMCheck() BillingEligibilityOption {
	return func(o *billingEligibilityOptions) {
		if o != nil {
			o.skipRPM = true
		}
	}
}

// WithMaxRequestSpend 声明本次请求的最坏费用上界（USD）。0 或负值不改变既有语义。
func WithMaxRequestSpend(amount float64) BillingEligibilityOption {
	return func(o *billingEligibilityOptions) {
		if o != nil && amount > 0 {
			o.maxRequestSpend = amount
		}
	}
}

// WithBalanceReservation 声明本次请求持有的"在途预留"槽位：预检放行时会绑定预留金额，
// 调用方需要在请求收尾时按路径归还（结算任务 Release / handler defer ReleaseOnExit）。
func WithBalanceReservation(slot *BillingReservationSlot) BillingEligibilityOption {
	return func(o *billingEligibilityOptions) {
		if o != nil && slot != nil {
			o.reservationSlot = slot
		}
	}
}

// CheckBillingEligibility 检查用户是否有资格发起请求
// 余额模式：检查缓存余额 > 0
// 订阅模式：检查缓存用量未超过限额（Group限额从参数传入）
// platform 为请求的目标平台（如 "anthropic"），传空串 "" 时跳过 user × platform quota 检查。
// opts 为可选的附加闸门（如 WithMaxRequestSpend 的最坏费用要求）。
func (s *BillingCacheService) CheckBillingEligibility(ctx context.Context, user *User, apiKey *APIKey, group *Group, subscription *UserSubscription, platform string, opts ...BillingEligibilityOption) error {
	defer requesttiming.Observe(ctx, "billing_check")()
	// 简易模式默认跳过所有计费检查. An explicit key-window opt-in keeps
	// balance/subscription/platform checks bypassed while enforcing the three
	// API-key monetary windows from the database source of truth.
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		if s.cfg.SimpleModeKeyRateLimitEnabled {
			return s.checkSimpleModeAPIKeyRateLimits(ctx, apiKey)
		}
		return nil
	}
	if s.circuitBreaker != nil && !s.circuitBreaker.Allow() {
		return ErrBillingServiceUnavailable
	}

	eligibility := billingEligibilityOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&eligibility)
		}
	}

	// 判断计费模式
	isSubscriptionMode := group != nil && group.IsSubscriptionType() && subscription != nil

	// 余额模式的权威余额（预检结论）：所有闸门通过后用于在途预留的封底护栏。
	var decisionBalance float64
	// 订阅模式的用量快照：用于在途预留的限额护栏（复用同一次缓存读，避免快照漂移）。
	var decisionSubData *subscriptionCacheData
	// 余额模式下 user×platform 配额的用量/限额快照：同样用于在途预留。
	var decisionPlatformQuota *userPlatformQuotaSnapshot

	if isSubscriptionMode {
		subData, err := s.checkSubscriptionEligibility(ctx, user.ID, group, subscription)
		if err != nil {
			return err
		}
		decisionSubData = subData
	} else {
		balance, err := s.checkBalanceEligibility(ctx, user.ID, eligibility.maxRequestSpend)
		if err != nil {
			return err
		}
		decisionBalance = balance
	}

	// user × platform quota 仅在 standard（余额）模式生效；订阅模式豁免
	if !isSubscriptionMode {
		platformQuota, err := s.loadUserPlatformQuotaEligibility(ctx, user.ID, platform)
		if err != nil {
			return err
		}
		decisionPlatformQuota = platformQuota
	}

	// Check API Key rate limits (applies to both billing modes)
	if apiKey != nil && apiKey.HasRateLimits() {
		if err := s.checkAPIKeyRateLimits(ctx, apiKey); err != nil {
			return err
		}
	}

	// RPM 限流：级联回落（Override → Group → User），放在最后以避免为注定失败的请求增加计数。
	if !eligibility.skipRPM {
		if err := s.checkRPM(ctx, user, group); err != nil {
			return err
		}
	}

	// 在途预留：所有闸门都通过后，把本次请求的最坏费用原子地记入对应额度的在途预留，
	// 并校验这份额度仍然放得起。
	//   - 余额模式：scope=用户，护栏是 `balance - Σ预留 >= reserve`；
	//   - 订阅模式：scope=用户×分组，护栏是 `usage + Σ预留 < 限额`（三个窗口任一超限即拒），
	//     修掉"并发请求共用同一份用量快照、超额消耗订阅配额"的超卖。
	// 放在最后一步：前面任何一步拒绝都无需回滚预留。
	//   - 余额模式还追加 user×platform quota 预留（scope=用户×平台）：任一窗口
	//     `usage + Σ预留` 越界即拒，堵住平台配额在并发下被超额消耗。
	//   - 两种模式都追加 API Key 额度预留（scope=apikey:<id>）：`quota_used + Σ预留 > quota`
	//     即拒，堵住同一个 key 的额度在并发下被超额消耗（参考 new-api 的 token 额度预留）。
	if isSubscriptionMode {
		if err := s.reserveSubscriptionSpend(ctx, user.ID, group, decisionSubData, eligibility.maxRequestSpend, eligibility.reservationSlot); err != nil {
			return err
		}
	} else {
		if err := s.reserveRequestSpend(ctx, user.ID, decisionBalance, eligibility.maxRequestSpend, eligibility.reservationSlot); err != nil {
			return err
		}
		// 平台配额是同一请求的第二个 scope：余额预留已经绑定，平台预留失败时必须
		// 回滚全部绑定，否则这笔被拒绝的请求会白占余额额度到 TTL。
		if err := s.reserveUserPlatformQuotaSpend(ctx, user.ID, platform, decisionPlatformQuota, eligibility.maxRequestSpend, eligibility.reservationSlot); err != nil {
			if eligibility.reservationSlot != nil {
				eligibility.reservationSlot.Release(ctx)
			}
			return err
		}
	}

	// API Key 总额度是同一请求可能追加的第三个 scope（余额/订阅两种模式都生效）：
	// 前面的绑定已经落下，这里失败同样必须整槽回滚，否则被拒请求会白占额度到 TTL。
	if err := s.reserveAPIKeyQuotaSpend(ctx, apiKey, eligibility.maxRequestSpend, eligibility.reservationSlot); err != nil {
		if eligibility.reservationSlot != nil {
			eligibility.reservationSlot.Release(ctx)
		}
		return err
	}

	return nil
}

// checkSimpleModeAPIKeyRateLimits is deliberately DB-authoritative. Redis
// updates are asynchronous and can be dropped or missed after a committed
// transaction, so using the cache here could let a limited simple-mode key
// continue past its configured window. A read failure fails closed because
// the operator explicitly opted into enforcement.
func (s *BillingCacheService) checkSimpleModeAPIKeyRateLimits(ctx context.Context, apiKey *APIKey) error {
	if apiKey == nil || !apiKey.HasRateLimits() {
		return nil
	}
	if s.apiKeyRateLimitLoader == nil {
		return ErrBillingServiceUnavailable
	}
	data, err := s.apiKeyRateLimitLoader.GetRateLimitData(ctx, apiKey.ID)
	if err != nil || data == nil {
		return ErrBillingServiceUnavailable
	}
	if apiKey.RateLimit5h > 0 && data.EffectiveUsage5h() >= apiKey.RateLimit5h {
		return ErrAPIKeyRateLimit5hExceeded
	}
	if apiKey.RateLimit1d > 0 && data.EffectiveUsage1d() >= apiKey.RateLimit1d {
		return ErrAPIKeyRateLimit1dExceeded
	}
	if apiKey.RateLimit7d > 0 && data.EffectiveUsage7d() >= apiKey.RateLimit7d {
		return ErrAPIKeyRateLimit7dExceeded
	}
	return nil
}

// checkRPM 执行并行 RPM 限流，所有适用的限制同时生效，任一超限即拒绝：
//
//  1. (用户, 分组) rpm_override       — 最细粒度：管理员为特定用户在特定分组设定的专属限额。
//     override=0 表示该用户在该分组免检（绿灯），但 user 级全局上限仍然生效。
//  2. group.rpm_limit                 — 分组级：该分组的统一 RPM 容量（仅当无 override 时生效）。
//  3. user.rpm_limit                  — 用户级全局硬上限：无论 override/group 如何配置，始终生效。
//
// 与旧版"级联互斥"设计不同，新版确保 user.rpm_limit 作为全局天花板不会被 group 或 override 覆盖。
// Redis 故障一律 fail-open（打 warning，不阻塞业务）。
func (s *BillingCacheService) checkRPM(ctx context.Context, user *User, group *Group) error {
	if s == nil || s.userRPMCache == nil || user == nil {
		return nil
	}

	// ── 第一层：分组级检查（override 或 group.rpm_limit） ──
	if group != nil {
		// 解析 override：优先从 auth cache snapshot，nil 时回退 DB。
		var override *int
		if user.UserGroupRPMOverride != nil {
			override = user.UserGroupRPMOverride
		} else if s.userGroupRateRepo != nil {
			dbOverride, err := s.userGroupRateRepo.GetRPMOverrideByUserAndGroup(ctx, user.ID, group.ID)
			if err != nil {
				logger.LegacyPrintf(
					"service.billing_cache",
					"Warning: rpm override lookup failed for user=%d group=%d: %v",
					user.ID, group.ID, err,
				)
			} else {
				override = dbOverride
			}
		}

		if override != nil {
			// override=0 → 该用户在该分组免检（但 user 级仍会在下面检查）。
			if *override > 0 {
				count, incErr := s.userRPMCache.IncrementUserGroupRPM(ctx, user.ID, group.ID)
				if incErr != nil {
					logger.LegacyPrintf(
						"service.billing_cache",
						"Warning: rpm increment (override) failed for user=%d group=%d: %v",
						user.ID, group.ID, incErr,
					)
					// fail-open
				} else if count > *override {
					return ErrGroupRPMExceeded
				}
			}
			// override 命中后跳过 group.rpm_limit（override 替代 group），但不 return——继续检查 user 级。
		} else if group.RPMLimit > 0 {
			// 无 override，检查 group.rpm_limit。
			count, err := s.userRPMCache.IncrementUserGroupRPM(ctx, user.ID, group.ID)
			if err != nil {
				logger.LegacyPrintf(
					"service.billing_cache",
					"Warning: rpm increment (group) failed for user=%d group=%d: %v",
					user.ID, group.ID, err,
				)
				// fail-open
			} else if count > group.RPMLimit {
				return ErrGroupRPMExceeded
			}
		}
	}

	// ── 第二层：用户级全局硬上限（始终生效） ──
	if user.RPMLimit > 0 {
		count, err := s.userRPMCache.IncrementUserRPM(ctx, user.ID)
		if err != nil {
			logger.LegacyPrintf(
				"service.billing_cache",
				"Warning: rpm increment (user) failed for user=%d: %v",
				user.ID, err,
			)
			return nil // fail-open
		}
		if count > user.RPMLimit {
			return ErrUserRPMExceeded
		}
	}

	return nil
}

func (s *BillingCacheService) minimumBalanceReserve() float64 {
	if s == nil || s.cfg == nil || s.cfg.Billing.MinimumBalanceReserve <= 0 {
		return 0
	}
	return s.cfg.Billing.MinimumBalanceReserve
}

// balanceExhaustion 返回底层缓存实现提供的"钱包已耗尽"标记读写能力。
// 未实现（例如测试用的轻量 stub）时返回 false，标记功能静默降级为 no-op，
// 预检仍然依靠余额缓存阈值把关。
func (s *BillingCacheService) balanceExhaustion() (balanceExhaustionStore, bool) {
	if s == nil || s.cache == nil {
		return nil, false
	}
	store, ok := s.cache.(balanceExhaustionStore)
	if !ok {
		return nil, false
	}
	return store, true
}

// MarkBalanceExhausted 打上"钱包已耗尽"标记。
//
// 只负责写标记，不负责失效余额缓存：缓存失效由调用方按各自语义决定——结算成功路径
// 已由 syncBalanceCacheAfterDeduction 失效，扣费被拒的失败路径会显式再失效一次，
// 而预检命中标记时也会顺手失效（自愈），避免把被旧回源值污染的缓存一直留着。
func (s *BillingCacheService) MarkBalanceExhausted(ctx context.Context, userID int64) {
	if s == nil {
		return
	}
	store, ok := s.balanceExhaustion()
	if !ok {
		return
	}
	if err := store.MarkUserBalanceExhausted(ctx, userID); err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: mark balance exhausted failed for user %d: %v", userID, err)
	}
}

// ClearBalanceExhausted 清除"钱包已耗尽"标记。所有让余额增加的路径都必须调用，
// 否则刚充值的用户会在标记 TTL 内继续被预检拦截。
func (s *BillingCacheService) ClearBalanceExhausted(ctx context.Context, userID int64) {
	if s == nil {
		return
	}
	ClearBalanceExhaustedMarker(ctx, s.cache, userID)
}

// ClearBalanceExhaustedMarker 供只持有 BillingCache 接口的调用方清除"钱包已耗尽"标记。
// 底层实现未提供该能力时静默 no-op。
func ClearBalanceExhaustedMarker(ctx context.Context, cache BillingCache, userID int64) {
	if cache == nil {
		return
	}
	store, ok := cache.(balanceExhaustionStore)
	if !ok {
		return
	}
	if err := store.ClearUserBalanceExhausted(ctx, userID); err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: clear balance exhausted failed for user %d: %v", userID, err)
	}
}

// InvalidateUserBalanceAfterCredit 在余额**增加**（充值 / 兑换 / 返利 / 管理员调整）后
// 统一失效余额缓存并清除"钱包已耗尽"标记。
func (s *BillingCacheService) InvalidateUserBalanceAfterCredit(ctx context.Context, userID int64) error {
	err := s.InvalidateUserBalance(ctx, userID)
	s.ClearBalanceExhausted(ctx, userID)
	return err
}

// balanceExhausted 查询"钱包已耗尽"标记。Redis 故障时返回 false（fail-open）：
// 此时余额阈值判断仍然生效，不能让一次 Redis 抖动把全体用户拦在门外。
func (s *BillingCacheService) balanceExhausted(ctx context.Context, userID int64) bool {
	store, ok := s.balanceExhaustion()
	if !ok {
		return false
	}
	exhausted, err := store.IsUserBalanceExhausted(ctx, userID)
	if err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: read balance exhausted marker failed for user %d: %v", userID, err)
		return false
	}
	return exhausted
}

// balanceNearEligibilityThreshold 判断缓存余额是否已经贴近 reserve 底线。
//
// 缓存余额只会在"未命中回源 + 异步写回"之间出现偏差，而真正危险、也真正需要
// 用 DB 真值复核的区间就是贴近底线的这一段；正常用户不为此多付一次 DB 查询。
func (s *BillingCacheService) balanceNearEligibilityThreshold(balance float64) bool {
	minimumReserve := s.minimumBalanceReserve()
	if minimumReserve <= 0 {
		return false
	}
	threshold := minimumReserve + s.balanceRecheckBand()
	if legacy := 2 * minimumReserve; threshold < legacy {
		threshold = legacy
	}
	return balance <= threshold
}

// balanceRecheckBand 返回预检 DB 复核带宽（billing.balance_recheck_band，美元）。
// 未配置或非法时为 0，此时退化为 2*reserve 的保守复核带。
func (s *BillingCacheService) balanceRecheckBand() float64 {
	if s == nil || s.cfg == nil || s.cfg.Billing.BalanceRecheckBand <= 0 {
		return 0
	}
	return s.cfg.Billing.BalanceRecheckBand
}

func (s *BillingCacheService) balanceBelowEligibilityThreshold(balance float64) bool {
	if balance <= 0 {
		return true
	}
	minimumReserve := s.minimumBalanceReserve()
	// 严格守卫：balance 必须严格大于 reserve（可花余额 balance - reserve > 0）。
	// 当 balance <= reserve 时，无可花额度，任何请求都无法通过扣费的 (balance >= amount + reserve) 门槛，
	// 此时必须在预检直接以 ErrInsufficientBalance 拦截 (403)，防止调用上游后扣费失败而产生未结算调用。
	return minimumReserve > 0 && balance <= minimumReserve
}

// checkBalanceEligibility 检查余额模式资格
//
// maxRequestSpend > 0 时在既有阈值之上追加"最坏费用闸门"：余额必须覆盖
// 封底线 + 本次最坏费用，否则 403 —— 付不满的请求绝不转发到上游。判定优先
// 使用 DB 真值（缓存值只用于筛选何时复核），因为缓存余额在并发扣费 + 异步
// 写回之间可能偏高。命中"付不满最坏费用"时不写"钱包已耗尽"标记：小额请求
// 仍可能付得起，标记会连带拦住它们；只有余额已掉到封底线（一切请求都付不起）
// 或结算路径判定耗尽时才打标记。
// 返回值是本次放行判定所依据的**权威余额**（必要时已用 DB 真值修正），供
// CheckBillingEligibility 汇合所有闸门后做在途预留的封底护栏；err != nil 时返回值无意义。
func (s *BillingCacheService) checkBalanceEligibility(ctx context.Context, userID int64, maxRequestSpend float64) (float64, error) {
	// 1) 先看"钱包已耗尽"标记。它是结算路径在"扣到底线 / 扣费被拒"时写下的权威信号，
	//    与余额缓存的回源写回无关，因此不会被旧余额快照复活。命中即 fail-closed：
	//    绝不再把注定扣费失败的请求转发到上游（后付费下上游成本无法追回）。
	if s.balanceExhausted(ctx, userID) {
		// 顺手失效余额缓存，让后续请求回源到真实余额，标记过期后立即恢复正常判断。
		if err := s.InvalidateUserBalance(ctx, userID); err != nil {
			logger.LegacyPrintf("service.billing_cache", "Warning: invalidate balance cache for exhausted user %d failed: %v", userID, err)
		}
		RecordBillingPreflightReject(BillingRejectMarkerActive)
		return 0, ErrInsufficientBalance
	}

	balance, err := s.GetUserBalance(ctx, userID)
	if err != nil {
		if s.circuitBreaker != nil {
			s.circuitBreaker.OnFailure(err)
		}
		logger.LegacyPrintf("service.billing_cache", "ALERT: billing balance check failed for user %d: %v", userID, err)
		return 0, ErrBillingServiceUnavailable.WithCause(err)
	}
	if s.circuitBreaker != nil {
		s.circuitBreaker.OnSuccess()
	}

	if s.balanceBelowEligibilityThreshold(balance) {
		RecordBillingPreflightReject(BillingRejectBelowReserve)
		return 0, ErrInsufficientBalance
	}

	// required 为本次放行要求的余额下限：封底线 + 最坏费用上界（未提供时为封底线
	// 本身，退化为既有语义）。maxRequestSpend 由 handler 经 WithMaxRequestSpend 传入。
	required := s.minimumBalanceReserve()
	if maxRequestSpend > 0 {
		required += maxRequestSpend
	}

	// 2) 余额贴近底线时，用 DB 真值复核一次。
	//    缓存余额在并发扣费 + 异步回源之间可能偏高，而"贴近底线"正是放行后会立刻
	//    扣不动钱的危险区间；这里多一次 PK 查询，换来"预检放行 ⇒ 结算必然可扣"。
	//    并发突发时同一用户的复核由 singleflight 合并为一次回源（见
	//    recheckUserBalanceFromDB），避免连接池被瞬时打满后 fail-closed 成大范围 503。
	//    只能读到 DB 时才复核：缺少 userRepo（部分降级/测试装配）时退回缓存判断。
	//    追加两种需要 DB 复核的情形：b) 缓存余额吃不下本次最坏费用（旧快照可能
	//    偏高，需用真值判定）；c) 扣掉本次最坏费用后贴近底线（放行即进入下一笔
	//    必然被拦的临界带）。
	needsRecheck := s.balanceNearEligibilityThreshold(balance)
	if maxRequestSpend > 0 && (balance < required || s.balanceNearEligibilityThreshold(balance-maxRequestSpend)) {
		needsRecheck = true
	}
	if s.userRepo == nil && needsRecheck {
		// 降级装配：读不到 DB 真值，预检只能依据可能偏高的缓存值。
		// 这是"护栏静默降级"的直接证据，必须计数，否则这种部署看起来一切正常。
		RecordBillingRecheckSkippedNoUserRepo()
	}
	if s.userRepo != nil && needsRecheck {
		RecordBillingRecheckDBRead()
		// 与回源写回同理：代号要在 DB 读取之前取（审计 R9）。
		recheckGeneration := s.balanceGenerationFor(ctx, userID)
		fresh, dbErr := s.recheckUserBalanceFromDB(userID)
		if dbErr != nil {
			// 无法确认真实余额时 fail-closed，避免继续白用上游。
			RecordBillingRecheckFailClosed()
			if s.circuitBreaker != nil {
				s.circuitBreaker.OnFailure(dbErr)
			}
			logger.LegacyPrintf("service.billing_cache", "ALERT: billing balance recheck failed for user %d: %v", userID, dbErr)
			return 0, ErrBillingServiceUnavailable.WithCause(dbErr)
		}
		if s.balanceBelowEligibilityThreshold(fresh) {
			s.MarkBalanceExhausted(ctx, userID)
			RecordBillingPreflightReject(BillingRejectDBTruthBelowReserve)
			return 0, ErrInsufficientBalance
		}
		// DB 真值仍可花：把缓存纠正为真值，消除旧快照带来的偏差。
		// 同样要带变动代号做条件写回：复核读值到写回之间若发生扣费，这份真值已经过期，
		// 直接发布会把偏高的余额写回缓存（审计 R9）。
		s.setBalanceCacheWithGeneration(ctx, userID, fresh, recheckGeneration)
		if maxRequestSpend > 0 && fresh < required {
			// 真值吃不下本次最坏费用：转发前拦截，避免上游成本发生后的坏账。
			// 不打"钱包已耗尽"标记——小额请求仍可能付得起，标记会连带拦住它们。
			RecordBillingPreflightReject(BillingRejectWorstCaseDBTruth)
			logger.LegacyPrintf("service.billing_cache",
				"billing preflight rejected user=%d: balance=%.6f < reserve=%.6f + worst_request_spend=%.6f",
				userID, fresh, s.minimumBalanceReserve(), maxRequestSpend)
			return 0, ErrInsufficientBalance
		}
		return fresh, nil
	}

	if maxRequestSpend > 0 && balance < required {
		// 无 userRepo（降级/测试装配）时只能依据缓存值：付不满最坏费用直接拒绝。
		RecordBillingPreflightReject(BillingRejectWorstCaseCache)
		logger.LegacyPrintf("service.billing_cache",
			"billing preflight rejected user=%d (cache only): balance=%.6f < reserve=%.6f + worst_request_spend=%.6f",
			userID, balance, s.minimumBalanceReserve(), maxRequestSpend)
		return 0, ErrInsufficientBalance
	}

	return balance, nil
}

// balanceReservation 返回底层缓存实现提供的"在途预留"读写能力。
// 未实现（例如测试用的轻量 stub）时返回 false，预留功能静默降级为 no-op，
// 预检仍然依靠余额阈值 + DB 真值复核 + "钱包已耗尽"标记把关。
func (s *BillingCacheService) balanceReservation() (billingReservationStore, bool) {
	if s == nil || s.cache == nil {
		return nil, false
	}
	store, ok := s.cache.(billingReservationStore)
	if !ok {
		return nil, false
	}
	return store, true
}

// reserveRequestSpend 在转发上游前，将本次请求的最坏费用上界原子地记入用户的
// "在途预留"，并校验"余额 - 预留总额 >= 封底"仍然成立。
//
// 该护栏把并发准入变成原子操作，使"预检放行 ⇒ 结算必然可扣"跨请求成立：
// 余额快照是静态的，而每个已放行请求未来都要从同一份余额里扣钱；在途预留把这份
// "已承诺但尚未结算"的额度从可花余额中扣除，只有拿得到额度的那一笔（或几笔）能
// 通过，其余在预检即 403。
//
// 每笔预留在 Redis 上都带一条以 requestID 为键的**凭据**（见 repository 层
// billingReservedItemKeyPrefix 的说明）：归还与续期都以凭据为准，避免晚到的归还
// 吃掉同一用户后来那笔请求的额度；bind 成功后启动心跳，超长请求不会被 TTL 掉保护。
//
// 失败语义：
//   - slot == nil（调用方未挂预留槽位）或 maxRequestSpend <= 0（没有可用上界）：no-op；
//   - 底层缓存不支持该能力（降级/轻量测试装配）：no-op；
//   - Redis 故障：首次切换到共享 DB 兜底账本；Redis 与 DB 都失败时 fail-closed（503），
//     只有未装配 DB 能力的装配才退回旧的 fail-open；
//   - 预留后封底护栏不成立：立即回滚预留并返回 ErrInsufficientBalance（403）。
func (s *BillingCacheService) reserveRequestSpend(ctx context.Context, userID int64, balance float64, maxRequestSpend float64, slot *BillingReservationSlot) error {
	scope := balanceReservationScope(userID)
	maxReserved := s.inflightReservationBudget(ctx, balance, maxRequestSpend)
	return s.reserveSpendWithGuard(ctx, scope, maxRequestSpend, maxReserved, slot, func(reservedAfter float64) error {
		if s.inflightReservationAllowed(ctx, balance, reservedAfter, maxRequestSpend) {
			return nil
		}
		// 护栏不成立：本次放行会击穿封底，立即回滚并拒绝。
		logger.LegacyPrintf("service.billing_cache",
			"billing preflight rejected user=%d (inflight reservation): balance=%.6f - reserved=%.6f < reserve=%.6f (budget_multiplier=%.3f)",
			userID, balance, reservedAfter, s.minimumBalanceReserve(), s.inflightReservationBudgetMultiplier(ctx))
		return ErrInsufficientBalance
	}, BillingRejectReservationGuard)
}

func (s *BillingCacheService) inflightReservationBudget(ctx context.Context, balance, maxRequestSpend float64) float64 {
	spendable := balance - s.minimumBalanceReserve()
	if spendable < 0 {
		spendable = 0
	}
	budget := spendable * s.inflightReservationBudgetMultiplier(ctx)
	if budget < maxRequestSpend {
		budget = maxRequestSpend
	}
	return budget
}

// defaultInflightReservationBudgetMultiplier 是在途预留聚合闸门的默认预算倍数。
const defaultInflightReservationBudgetMultiplier = 1.0

// inflightReservationBudgetResolver 是后台设置（/admin/settings）里的预算倍数解析器。
// 由 wire 在启动时注入 SettingService.GetInflightReservationBudgetMultiplier（自带 60s
// 进程内缓存），因此热路径不会触库。沿用包内既有的解析器注入约定（见
// SetCodexCanonicalUserAgentResolver）：避免为一个运行时开关改动 BillingCacheService
// 的构造签名（有 ~50 处调用点）。
var inflightReservationBudgetResolver func(context.Context) float64

// SetInflightReservationBudgetResolver 注入后台设置解析器（nil = 仅用静态配置）。
func SetInflightReservationBudgetResolver(fn func(context.Context) float64) {
	inflightReservationBudgetResolver = fn
}

// inflightReservationBudgetMultiplier 返回在途预留聚合闸门的预算倍数：
// 允许「可花余额」被在途预留总额覆盖的倍数。1.0 = 严格（预留总额不得超过可花余额，
// 坏账压到 0）。调高可放宽并发准入，代价是允许 shortfall 坏账
// （见 config.BillingConfig.InflightReservationBudgetMultiplier 注释）。
//
// 取值优先级：后台设置（合法且 >=1）> 静态配置（合法且 >=1）> 1.0。任何一级出现非法值
// 都回退到下一级，绝不因为一个坏值把准入放宽成危险模式。
func (s *BillingCacheService) inflightReservationBudgetMultiplier(ctx context.Context) float64 {
	if fn := inflightReservationBudgetResolver; fn != nil {
		if v := fn(ctx); v >= 1 {
			return v
		}
	}
	if s == nil || s.cfg == nil ||
		s.cfg.Billing.InflightReservationBudgetMultiplier < 1 {
		return defaultInflightReservationBudgetMultiplier
	}
	return s.cfg.Billing.InflightReservationBudgetMultiplier
}

// inflightReservationAllowed 判定「在途预留总额」是否仍在预算内。
//
// 严格模式（倍数 = 1）等价于 balance - reservedAfter >= reserve：预留总额不得超过
// 可花余额，因此在途请求全部按最坏费用结算也不会击穿封底，零坏账。
//
// 放宽模式（倍数 > 1）把上限改为 spendable × 倍数，于是并发准入不再被
// (可花余额 / 单笔最坏费用) 卡住，语义变为「只要这一笔的最坏费用付得起就放行，
// 直到余额花到封底为止」——单笔最坏费用闸门（checkBalanceEligibility 的 required）
// 依然逐个请求生效，所以停止点仍是封底。代价是在途实际总花费可能超过可花余额，
// 超出部分在结算时记为 shortfall（余额由结算 SQL 夹在封底之上，绝不为负）。
//
// 预算至少放行一笔：否则倍数退化时第一笔会被自己的预留挡住。
func (s *BillingCacheService) inflightReservationAllowed(ctx context.Context, balance, reservedAfter, maxRequestSpend float64) bool {
	reserve := s.minimumBalanceReserve()
	budget := s.inflightReservationBudget(ctx, balance, maxRequestSpend)
	if reservedAfter <= budget {
		return true
	}
	// 严格模式下保持原有的封底判据，避免浮点/阈值语义漂移。
	return balance-reservedAfter >= reserve
}

// reserveSpendWithGuard 是"在途预留"的通用实现：把本次请求的最坏费用原子地记入
// scope 的在途预留总额，再交给 guard 判定这份额度是否仍然放得起。
//
// 余额模式与订阅模式共用它，只是 scope 与 guard 不同：
//   - 余额模式：scope=userID，guard 检查 `balance - reserved >= reserve`；
//   - 订阅模式：scope=user:group，guard 检查 `usage + reserved <= daily/weekly/monthly limit`。
//
// guard 返回错误即回滚预留并原样抛出该错误（保持各模式既有的错误码与 HTTP 形状）。
//
// 失败语义：
//   - slot == nil（调用方未挂预留槽位）或 amount <= 0（没有可用上界）：no-op；
//   - 底层缓存不支持该能力（降级/轻量测试装配）：no-op；
//   - Redis 故障：切到共享 DB 兜底；Redis 与 DB 都失败时 fail-closed（503），仅无 DB 装配
//     的降级/测试场景保留旧 fail-open；
//   - guard 判定不成立：回滚预留 + 计数，返回 guard 的错误。
func (s *BillingCacheService) reserveSpendWithGuard(
	ctx context.Context,
	scope string,
	amount float64,
	maxTotal float64,
	slot *BillingReservationSlot,
	guard func(reservedAfter float64) error,
	rejectReason BillingPreflightRejectReason,
) error {
	if s == nil || slot == nil || amount <= 0 || scope == "" || guard == nil {
		return nil
	}
	store, ok := s.balanceReservation()
	if !ok {
		return nil
	}
	requestID := newBillingReservationRequestID()
	if atomicStore, atomicOK := store.(atomicBillingReservationStore); atomicOK {
		reservedAfter, accepted, err := atomicStore.TryReserveUserBalance(ctx, scope, requestID, amount, maxTotal, billingReservationTTL)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if errors.Is(err, ErrReservationBackendsUnavailable) {
				RecordBillingReservationFailClosed()
				logger.LegacyPrintf("service.billing_cache", "ALERT: Redis and database reservation backends are unavailable for scope %s: %v", scope, err)
				return ErrBillingServiceUnavailable.WithCause(err)
			}
			RecordBillingReservationFailOpen()
			logger.LegacyPrintf("service.billing_cache", "ALERT: reserve in-flight spend for scope %s failed: %v", scope, err)
			return nil
		}
		if !accepted {
			RecordBillingReservationRejected()
			if rejectReason != "" {
				RecordBillingPreflightReject(rejectReason)
			}
			return guard(reservedAfter + amount)
		}
		slot.bind(store, scope, requestID, amount)
		slot.startHeartbeat()
		RecordBillingReservationReserved()
		return nil
	}
	reservedAfter, err := store.ReserveUserBalance(ctx, scope, requestID, amount, billingReservationTTL)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(err, ErrReservationBackendsUnavailable) {
			RecordBillingReservationFailClosed()
			logger.LegacyPrintf("service.billing_cache", "ALERT: Redis and database reservation backends are unavailable for scope %s: %v", scope, err)
			return ErrBillingServiceUnavailable.WithCause(err)
		}
		RecordBillingReservationFailOpen()
		logger.LegacyPrintf("service.billing_cache", "ALERT: reserve in-flight spend for scope %s failed: %v", scope, err)
		return nil // fail-open
	}
	if guardErr := guard(reservedAfter); guardErr != nil {
		// 回滚走脱离取消的独立 ctx：请求 ctx 可能已经结束，直接用它会让 Redis 写入静默失败。
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), billingReservationReleaseTimeout)
		defer cancel()
		if relErr := store.ReleaseUserBalanceReservation(rollbackCtx, scope, requestID, amount, billingReservationTTL); relErr != nil {
			if errors.Is(relErr, ErrBillingReservationExpired) {
				RecordBillingReservationExpiredRelease()
			} else {
				RecordBillingReservationReleaseError()
				logger.LegacyPrintf("service.billing_cache", "ALERT: rollback in-flight reservation for scope %s failed: %v", scope, relErr)
			}
		}
		RecordBillingReservationRejected()
		if rejectReason != "" {
			RecordBillingPreflightReject(rejectReason)
		}
		return guardErr
	}
	slot.bind(store, scope, requestID, amount)
	slot.startHeartbeat()
	RecordBillingReservationReserved()
	return nil
}

// subscriptionLimitExceeded 判定"订阅剩余限额是否还吃得起在途预留"。
//
// 语义与余额模式一致：把"已放行但未结算"的最坏费用也算进已用量，只有
// `usage + inFlight` 仍在限额内才继续放行；否则本次请求在转发前就被拦下，
// 不会出现"配额用超了才发现"的超卖。
//
// 两个条件缺一不可，边界由此明确：
//   - `usage >= limit`：沿用既有拒绝边界（已达限额即拒）。少了它，未挂最坏费用上界
//     （inFlight 恒为 0）的调用会从 `>=` 退化成 `>`，允许"usage 正好等于限额"时再放行
//     一笔 —— 那就是净新增的超发。
//   - `usage + inFlight > limit`：有了在途预留后不得越界。用 `>` 而非 `>=` 是有意的：
//     "剩余额度正好等于最坏费用"应当放行（最坏情况全部花光也刚好落在限额上，不越界），
//     用 `>=` 会把这类请求误拦。
func subscriptionLimitExceeded(group *Group, subData *subscriptionCacheData, inFlight float64) error {
	if group == nil || subData == nil {
		return nil
	}
	if inFlight < 0 {
		inFlight = 0
	}
	if group.HasDailyLimit() && subscriptionWindowExceeded(subData.DailyUsage, *group.DailyLimitUSD, inFlight) {
		return ErrDailyLimitExceeded
	}
	if group.HasWeeklyLimit() && subscriptionWindowExceeded(subData.WeeklyUsage, *group.WeeklyLimitUSD, inFlight) {
		return ErrWeeklyLimitExceeded
	}
	if group.HasMonthlyLimit() && subscriptionWindowExceeded(subData.MonthlyUsage, *group.MonthlyLimitUSD, inFlight) {
		return ErrMonthlyLimitExceeded
	}
	return nil
}

// subscriptionWindowExceeded 判定单个限额窗口是否被击穿（见 subscriptionLimitExceeded 注释）。
func subscriptionWindowExceeded(usage, limit, inFlight float64) bool {
	if usage >= limit {
		return true
	}
	return usage+inFlight > limit
}

// reserveSubscriptionSpend 为订阅模式建立在途预留：把本次最坏费用记入
// user × group 的预留总额，并校验三个窗口的剩余限额都还够。
//
// 未配置任何限额（不限量的订阅）、无最坏费用上界或缓存不支持预留能力时静默 no-op，
// 行为与修复前一致。
func (s *BillingCacheService) reserveSubscriptionSpend(ctx context.Context, userID int64, group *Group, subData *subscriptionCacheData, maxRequestSpend float64, slot *BillingReservationSlot) error {
	if group == nil || subData == nil {
		return nil
	}
	if !group.HasDailyLimit() && !group.HasWeeklyLimit() && !group.HasMonthlyLimit() {
		return nil
	}
	scope := subscriptionReservationScope(userID, group.ID)
	maxReserved := subscriptionReservationBudget(group, subData)
	return s.reserveSpendWithGuard(ctx, scope, maxRequestSpend, maxReserved, slot, func(reservedAfter float64) error {
		if err := subscriptionLimitExceeded(group, subData, reservedAfter); err != nil {
			logger.LegacyPrintf("service.billing_cache",
				"billing preflight rejected user=%d group=%d (subscription inflight reservation): reserved=%.6f would exceed limit",
				userID, group.ID, reservedAfter)
			return err
		}
		return nil
	}, "")
}

func subscriptionReservationBudget(group *Group, subData *subscriptionCacheData) float64 {
	// Redis Lua receives this value as a string; +Inf is not portable across
	// Redis/Lua versions, so keep the no-limit sentinel finite.
	budget := math.MaxFloat64
	if group.HasDailyLimit() {
		budget = math.Min(budget, math.Max(0, *group.DailyLimitUSD-subData.DailyUsage))
	}
	if group.HasWeeklyLimit() {
		budget = math.Min(budget, math.Max(0, *group.WeeklyLimitUSD-subData.WeeklyUsage))
	}
	if group.HasMonthlyLimit() {
		budget = math.Min(budget, math.Max(0, *group.MonthlyLimitUSD-subData.MonthlyUsage))
	}
	return budget
}

// reserveUserPlatformQuotaSpend 为余额模式追加 user×platform 配额的在途预留：
// 把本次最坏费用记入 user×platform 的预留总额，并要求三个窗口的
// `usage + Σ预留` 都不越界。只在该 user×platform 配了至少一个 limit 时生效。
//
// 与订阅预留共用同一套 reserveSpendWithGuard 原子语义；scope 独立，因此余额
// 护栏与平台配额护栏互不影响，但同一次请求会在槽位上持有两条凭据。
func (s *BillingCacheService) reserveUserPlatformQuotaSpend(ctx context.Context, userID int64, platform string, quota *userPlatformQuotaSnapshot, maxRequestSpend float64, slot *BillingReservationSlot) error {
	if quota == nil || platform == "" || !quota.hasLimit() {
		return nil
	}
	scope := userPlatformQuotaReservationScope(userID, platform)
	maxReserved := quota.reservationBudget()
	return s.reserveSpendWithGuard(ctx, scope, maxRequestSpend, maxReserved, slot, func(reservedAfter float64) error {
		if err := quota.limitExceeded(reservedAfter); err != nil {
			logger.LegacyPrintf("service.billing_cache",
				"billing preflight rejected user=%d platform=%s (platform quota inflight reservation): reserved=%.6f would exceed limit",
				userID, platform, reservedAfter)
			return err
		}
		return nil
	}, "")
}

// reserveAPIKeyQuotaSpend 为 API Key 总额度（api_key.quota）追加在途预留：
// 把本次最坏费用记入 scope=apikey:<id> 的预留总额，要求 `quota_used + Σ预留 <= quota`。
// 两种计费模式（余额 / 订阅）都生效 —— key 额度与钱包、订阅是并行的三道额度。
//
// 参考 new-api 的 token 额度预留（model.TryReserveTokenQuota 同时动 RemainQuota 与
// UsedQuota，Redis 不可用时回落 reserveTokenQuotaDB 的条件更新）：sub2api 的准入只读
// 鉴权缓存里的 quota_used 快照，而 quota_used 要到结算路径
// （APIKeyService.UpdateQuotaUsed）才原子递增，因此并发突发时 N 个请求会共用同一份
// 快照一起穿过 `quota_used >= quota` 判定，使该 key 的 quota_used 超出 quota
// （钱照收，额度上限失效）。把"已放行未结算"计入后，判定与余额/订阅预留同构。
//
// 未配置额度（quota <= 0 = 不限量）、无最坏费用上界或缓存不支持预留能力时静默 no-op，
// 行为与修复前一致。
func (s *BillingCacheService) reserveAPIKeyQuotaSpend(ctx context.Context, apiKey *APIKey, maxRequestSpend float64, slot *BillingReservationSlot) error {
	if apiKey == nil || apiKey.ID <= 0 || apiKey.Quota <= 0 {
		return nil
	}
	// 快照化：护栏闭包里不再读 apiKey，避免调用方并发改动同一对象造成判定漂移。
	apiKeyID := apiKey.ID
	limit := apiKey.Quota
	used := apiKey.QuotaUsed
	// 叠加"已结算用量"共享账本：鉴权缓存里的 quota_used 是快照（L1 15s / L2 300s），
	// 在快照过期前它是冻结的，而结算只递增 DB。若只看快照，等此前放行的请求结算完
	// 并归还完在途预留，预留总额归零而快照仍旧，同一个 key 就能把一个额度周期内的
	// 剩余额度重复放行一遍（修复前的超发根因）。账本是单调高水位且恒 ≤ DB 真值，
	// 因此 max(快照, 账本) 只会让准入更保守。
	if ledgerUsed, ok := s.apiKeyQuotaUsedHighWater(ctx, apiKeyID); ok && ledgerUsed > used {
		// 观察到账本高水位领先快照：正是"快照冻结"缺陷的现场，计数暴露给 ops。
		RecordBillingAPIKeyQuotaLedgerStaleRescue()
		used = ledgerUsed
	}
	remaining := math.Max(0, limit-used)
	scope := apiKeyQuotaReservationScope(apiKeyID)
	return s.reserveSpendWithGuard(ctx, scope, maxRequestSpend, remaining, slot, func(reservedAfter float64) error {
		// used >= limit 沿用既有拒绝边界（无预留时的 `quota_used >= quota`）；
		// 第二项是并发维度：最坏情况全部花光也不得越过额度上限。
		if used >= limit || used+reservedAfter > limit {
			logger.LegacyPrintf("service.billing_cache",
				"billing preflight rejected api_key=%d (quota inflight reservation): used=%.6f reserved=%.6f would exceed quota=%.6f",
				apiKeyID, used, reservedAfter, limit)
			return ErrAPIKeyQuotaExhausted
		}
		return nil
	}, "")
}

// apiKeyQuotaUsedHighWater 读取 API Key 已用额度共享账本的高水位。
//
// 读失败时返回 ok=false 并计数 + ALERT：此时准入退回"仅鉴权快照"，与修复前一致
// （方向偏松）。之所以不 fail-closed：账本与在途预留共用同一条 Redis，Redis 故障时
// 预留本身会切到 DB 兜底账本（另有 fail-closed 路径），这里再返回 503 只会把一次
// Redis 抖动放大成"所有配额 key 全部不可用"。失败已经被计数暴露在 ops 面板上。
func (s *BillingCacheService) apiKeyQuotaUsedHighWater(ctx context.Context, apiKeyID int64) (float64, bool) {
	store := apiKeyQuotaUsedLedgerStore
	if store == nil || apiKeyID <= 0 {
		return 0, false
	}
	used, err := store.GetAPIKeyQuotaUsedLedger(ctx, apiKeyID)
	if err != nil {
		RecordBillingAPIKeyQuotaLedgerReadError()
		logger.LegacyPrintf("service.billing_cache",
			"ALERT: read api key quota used ledger failed key=%d: %v (falling back to auth snapshot quota_used)",
			apiKeyID, err)
		return 0, false
	}
	if used <= 0 {
		return 0, false
	}
	return used, true
}

// checkSubscriptionEligibility 检查订阅模式资格，并返回订阅缓存数据。
//
// 返回 subData 是为了让调用方能在放行前用同一份快照建立在途预留
// （见 reserveSubscriptionSpend）：再查一次缓存既浪费一次读，也可能读到不同的
// 用量快照，使"判定的依据"和"预留的依据"不一致。
func (s *BillingCacheService) checkSubscriptionEligibility(ctx context.Context, userID int64, group *Group, subscription *UserSubscription) (*subscriptionCacheData, error) {
	// 获取订阅缓存数据
	subData, err := s.GetSubscriptionStatus(ctx, userID, group.ID)
	if err != nil {
		if s.circuitBreaker != nil {
			s.circuitBreaker.OnFailure(err)
		}
		logger.LegacyPrintf("service.billing_cache", "ALERT: billing subscription check failed for user %d group %d: %v", userID, group.ID, err)
		return nil, ErrBillingServiceUnavailable.WithCause(err)
	}
	if s.circuitBreaker != nil {
		s.circuitBreaker.OnSuccess()
	}

	// 检查订阅状态
	if subData.Status != SubscriptionStatusActive {
		return nil, ErrSubscriptionInvalid
	}

	// 检查是否过期
	if time.Now().After(subData.ExpiresAt) {
		return nil, ErrSubscriptionInvalid
	}

	// 检查限额（使用传入的Group限额配置）。在途预留为 0：并发维度的护栏在
	// CheckBillingEligibility 里通过 reserveSubscriptionSpend 统一叠加。
	if err := subscriptionLimitExceeded(group, subData, 0); err != nil {
		return nil, err
	}

	return subData, nil
}

type billingCircuitBreakerState int

const (
	billingCircuitClosed billingCircuitBreakerState = iota
	billingCircuitOpen
	billingCircuitHalfOpen
)

type billingCircuitBreaker struct {
	mu                sync.Mutex
	state             billingCircuitBreakerState
	failures          int
	openedAt          time.Time
	failureThreshold  int
	resetTimeout      time.Duration
	halfOpenRequests  int
	halfOpenRemaining int
}

func newBillingCircuitBreaker(cfg config.CircuitBreakerConfig) *billingCircuitBreaker {
	if !cfg.Enabled {
		return nil
	}
	resetTimeout := time.Duration(cfg.ResetTimeoutSeconds) * time.Second
	if resetTimeout <= 0 {
		resetTimeout = 30 * time.Second
	}
	halfOpen := cfg.HalfOpenRequests
	if halfOpen <= 0 {
		halfOpen = 1
	}
	threshold := cfg.FailureThreshold
	if threshold <= 0 {
		threshold = 5
	}
	return &billingCircuitBreaker{
		state:            billingCircuitClosed,
		failureThreshold: threshold,
		resetTimeout:     resetTimeout,
		halfOpenRequests: halfOpen,
	}
}

func (b *billingCircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case billingCircuitClosed:
		return true
	case billingCircuitOpen:
		if time.Since(b.openedAt) < b.resetTimeout {
			return false
		}
		b.state = billingCircuitHalfOpen
		b.halfOpenRemaining = b.halfOpenRequests
		logger.LegacyPrintf("service.billing_cache", "ALERT: billing circuit breaker entering half-open state")
		fallthrough
	case billingCircuitHalfOpen:
		if b.halfOpenRemaining <= 0 {
			return false
		}
		b.halfOpenRemaining--
		return true
	default:
		return false
	}
}

func (b *billingCircuitBreaker) OnFailure(err error) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case billingCircuitOpen:
		return
	case billingCircuitHalfOpen:
		b.state = billingCircuitOpen
		b.openedAt = time.Now()
		b.halfOpenRemaining = 0
		logger.LegacyPrintf("service.billing_cache", "ALERT: billing circuit breaker opened after half-open failure: %v", err)
		return
	default:
		b.failures++
		if b.failures >= b.failureThreshold {
			b.state = billingCircuitOpen
			b.openedAt = time.Now()
			b.halfOpenRemaining = 0
			logger.LegacyPrintf("service.billing_cache", "ALERT: billing circuit breaker opened after %d failures: %v", b.failures, err)
		}
	}
}

func (b *billingCircuitBreaker) OnSuccess() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	previousState := b.state
	previousFailures := b.failures

	b.state = billingCircuitClosed
	b.failures = 0
	b.halfOpenRemaining = 0

	// 只有状态真正发生变化时才记录日志
	if previousState != billingCircuitClosed {
		logger.LegacyPrintf("service.billing_cache", "ALERT: billing circuit breaker closed (was %s)", circuitStateString(previousState))
	} else if previousFailures > 0 {
		logger.LegacyPrintf("service.billing_cache", "INFO: billing circuit breaker failures reset from %d", previousFailures)
	}
}

func circuitStateString(state billingCircuitBreakerState) string {
	switch state {
	case billingCircuitClosed:
		return "closed"
	case billingCircuitOpen:
		return "open"
	case billingCircuitHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// userPlatformQuotaSnapshot 是一次预检读到的 user×platform 配额快照。
// 返回快照（而不只是 error）是为了让调用方在放行前用同一次缓存读建立
// 在途预留，避免"判定依据"与"预留依据"来自两份不同的用量快照。
type userPlatformQuotaSnapshot struct {
	dailyLimitUSD   *float64
	weeklyLimitUSD  *float64
	monthlyLimitUSD *float64

	dailyUsageUSD   float64
	weeklyUsageUSD  float64
	monthlyUsageUSD float64

	dailyWindowStart   *time.Time
	weeklyWindowStart  *time.Time
	monthlyWindowStart *time.Time
}

func (s *userPlatformQuotaSnapshot) hasLimit() bool {
	return s != nil && (s.dailyLimitUSD != nil || s.weeklyLimitUSD != nil || s.monthlyLimitUSD != nil)
}

// reservationBudget 返回三个窗口中最小的剩余额度（未配置 limit 的窗口不参与）。
func (s *userPlatformQuotaSnapshot) reservationBudget() float64 {
	if s == nil {
		return 0
	}
	budget := math.MaxFloat64
	if s.dailyLimitUSD != nil {
		budget = math.Min(budget, math.Max(0, *s.dailyLimitUSD-s.dailyUsageUSD))
	}
	if s.weeklyLimitUSD != nil {
		budget = math.Min(budget, math.Max(0, *s.weeklyLimitUSD-s.weeklyUsageUSD))
	}
	if s.monthlyLimitUSD != nil {
		budget = math.Min(budget, math.Max(0, *s.monthlyLimitUSD-s.monthlyUsageUSD))
	}
	return budget
}

// limitExceeded 判定"已用 + 在途预留"是否击穿任一窗口限额。
// inFlight=0 时退化为既有 `usage >= limit` 的拒绝边界；inFlight>0 时要求
// `usage + inFlight <= limit`，与订阅预留共用同一套边界语义。
func (s *userPlatformQuotaSnapshot) limitExceeded(inFlight float64) error {
	if s == nil {
		return nil
	}
	if inFlight < 0 {
		inFlight = 0
	}
	now := time.Now()
	if s.dailyLimitUSD != nil && subscriptionWindowExceeded(s.dailyUsageUSD, *s.dailyLimitUSD, inFlight) {
		return withWindowResetsMetadata(ErrUserPlatformDailyQuotaExhausted, nextDailyReset(now))
	}
	if s.weeklyLimitUSD != nil && subscriptionWindowExceeded(s.weeklyUsageUSD, *s.weeklyLimitUSD, inFlight) {
		return withWindowResetsMetadata(ErrUserPlatformWeeklyQuotaExhausted, nextWeeklyReset(now))
	}
	if s.monthlyLimitUSD != nil && subscriptionWindowExceeded(s.monthlyUsageUSD, *s.monthlyLimitUSD, inFlight) {
		return withWindowResetsMetadata(ErrUserPlatformMonthlyQuotaExhausted, nextMonthlyResetFrom(s.monthlyWindowStart, now))
	}
	return nil
}

// loadUserPlatformQuotaEligibility 在 standard 模式下检查 user × platform 日/周/月 quota，
// 并把判定用的用量/限额快照返回给调用方（供在途预留复用，见 reserveUserPlatformQuotaSpend）。
//
// 返回 nil = 允许；返回 ErrUserPlatform{Daily/Weekly/Monthly}QuotaExhausted = 拒绝（带 window_resets_at metadata）。
//
// 流程（Redis-first / DB-fallback）：
//  1. 先读 Redis cache；若命中且 SchemaVersion==1，直接用 entry 中的 limits 和 window_start 做校验，
//     免除 DB 查询。
//  2. cache MISS 或旧版 entry（SchemaVersion==0）→ 查 DB 回填完整 entry（含 limits/window_start）。
//  3. Redis 故障（err != nil）→ fail-open，查 DB 做一次性检查，不回填。
func (s *BillingCacheService) loadUserPlatformQuotaEligibility(
	ctx context.Context,
	userID int64,
	platform string,
) (*userPlatformQuotaSnapshot, error) {
	if platform == "" || s.userPlatformQuotaRepo == nil {
		return nil, nil
	}

	// cache 未配置（如简化部署 / 单测路径）→ 直接走 DB 查询，避免 nil panic。
	// 其他 check* 方法（balance/subscription/rate-limit）也有类似守卫。
	var (
		entry    *UserPlatformQuotaCacheEntry
		ok       bool
		cacheErr error
	)
	if s.cache != nil {
		entry, ok, cacheErr = s.cache.GetUserPlatformQuotaCache(ctx, userID, platform)
	} else {
		// 标记为"cache 故障"分支：跳过 HIT 路径、不回填、走 DB 一次性检查
		cacheErr = errBillingCacheUnavailable
	}

	// --- cache HIT with current schema → 直接用 entry，不查 DB ---
	if cacheErr == nil && ok && entry != nil && entry.SchemaVersion == UserPlatformQuotaCacheSchemaV1 {
		now := time.Now()
		dailyUsage := entry.DailyUsageUSD
		weeklyUsage := entry.WeeklyUsageUSD
		monthlyUsage := entry.MonthlyUsageUSD
		// 若窗口已更新（DB 已重置但 cache 尚未失效）,将对应 usage 清零再做比较,
		// 同时记录新窗口起点用于后续刷新 cache entry。
		// 本次请求用本地清零值继续判断;DB 层 IncrementUsageWithReset 已有窗口自愈能力,
		// 持久化数据始终正确。
		windowExpired := false
		newDailyStart := entry.DailyWindowStart
		newWeeklyStart := entry.WeeklyWindowStart
		newMonthlyStart := entry.MonthlyWindowStart
		if quotaWindowExpired(entry.DailyWindowStart, timezone.StartOfDay(now)) {
			dailyUsage = 0
			windowExpired = true
			dayStart := timezone.StartOfDay(now)
			newDailyStart = &dayStart
		}
		if quotaWindowExpired(entry.WeeklyWindowStart, timezone.StartOfWeek(now)) {
			weeklyUsage = 0
			windowExpired = true
			weekStart := timezone.StartOfWeek(now)
			newWeeklyStart = &weekStart
		}
		if monthlyQuotaWindowExpired(entry.MonthlyWindowStart, now) {
			monthlyUsage = 0
			windowExpired = true
			monthStart := now
			newMonthlyStart = &monthStart
		}
		// 检测到任意窗口过期：用 reset 后的 entry 覆盖 Redis（而非 Delete）。
		// 旧实现 Delete 后,期间到达的 IncrUserPlatformQuotaUsage 调用让 Lua 看到
		// EXISTS=0 直接 return 0,并发请求的 cost 永久丢失,直到下次 cache MISS 回填。
		// 改为 SetCache 原子覆盖:key 不断链,Lua INCR 可在新窗口 entry 上正确累加。
		// 超时 50ms:覆盖正常路径与可接受抖动;Redis 异常时 hot path 不阻塞超过此值。
		// 用 context.Background()+短超时,避免请求 ctx 取消导致刷新丢失。
		// 显式 setCancel()(而非 defer):缩短 context 生命周期,避免 defer 延迟到函数返回。
		// isSentinel 判定「该 entry 无任何 limit」(DB 无行时回填的短 TTL sentinel),跨窗口命中时跳过 refresh:
		// refresh 会把短 TTL 误升级为 86400s;其 enforcement(下方 limit!=nil 比较)因 limit 全 nil 永远放行,
		// 跳过 refresh 不改变结果。
		isSentinel := entry.DailyLimitUSD == nil && entry.WeeklyLimitUSD == nil && entry.MonthlyLimitUSD == nil
		if windowExpired && s.cache != nil && !isSentinel {
			refreshed := &UserPlatformQuotaCacheEntry{
				DailyUsageUSD:      dailyUsage,
				WeeklyUsageUSD:     weeklyUsage,
				MonthlyUsageUSD:    monthlyUsage,
				SchemaVersion:      UserPlatformQuotaCacheSchemaV1,
				DailyLimitUSD:      entry.DailyLimitUSD,
				WeeklyLimitUSD:     entry.WeeklyLimitUSD,
				MonthlyLimitUSD:    entry.MonthlyLimitUSD,
				DailyWindowStart:   newDailyStart,
				WeeklyWindowStart:  newWeeklyStart,
				MonthlyWindowStart: newMonthlyStart,
			}
			ttl := time.Duration(s.cfg.Billing.UserPlatformQuotaCacheTTLSeconds) * time.Second
			setCtx, setCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			if setErr := s.cache.SetUserPlatformQuotaCache(setCtx, userID, platform, refreshed, ttl); setErr != nil {
				logger.LegacyPrintf("service.billing_cache",
					"Warning: refresh expired user platform quota cache failed user=%d platform=%s: %v",
					userID, platform, setErr)
			}
			setCancel()
		}
		snapshot := &userPlatformQuotaSnapshot{
			dailyLimitUSD:      entry.DailyLimitUSD,
			weeklyLimitUSD:     entry.WeeklyLimitUSD,
			monthlyLimitUSD:    entry.MonthlyLimitUSD,
			dailyUsageUSD:      dailyUsage,
			weeklyUsageUSD:     weeklyUsage,
			monthlyUsageUSD:    monthlyUsage,
			dailyWindowStart:   newDailyStart,
			weeklyWindowStart:  newWeeklyStart,
			monthlyWindowStart: newMonthlyStart,
		}
		return snapshot, snapshot.limitExceeded(0)
	}

	// --- cache MISS、旧版 entry 或 Redis 故障 → 查 DB（singleflight 合并并发回源）---
	// 使用 DoChan 而非 Do：avoid sharing the first caller's ctx among all dedupe followers.
	// 若第一个 caller 的 ctx 被取消（客户端断连），后续 caller 不受影响，仍由各自 ctx 控制超时。
	sfKey := strconv.FormatInt(userID, 10) + ":" + platform
	ch := s.quotaLoadSF.DoChan(sfKey, func() (any, error) {
		// 子查询用 detached context + 短超时，独立于任何 caller 的请求 ctx，
		// 防止"第一个 caller ctx 取消"使所有 follower 一起 fail。
		bgCtx, bgCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer bgCancel()
		return s.userPlatformQuotaRepo.GetByUserPlatform(bgCtx, userID, platform)
	})
	var (
		v     any
		dbErr error
	)
	select {
	case res := <-ch:
		v, dbErr = res.Val, res.Err
	case <-ctx.Done():
		// 当前 caller 的 ctx 被取消：fail-open，不阻断 (此请求已无意义)。
		logger.LegacyPrintf("service.billing_cache", "Warning: user platform quota check ctx cancelled user=%d platform=%s: %v (fail-open)", userID, platform, ctx.Err())
		return nil, nil
	}
	if dbErr != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: load user platform quota failed user=%d platform=%s: %v (fail-open)", userID, platform, dbErr)
		return nil, nil
	}
	rec, _ := v.(*UserPlatformQuotaRecord)
	if rec == nil {
		// 仅在 cache 可用且本次 GET 未出错时回填 sentinel:Redis GET 故障(cacheErr!=nil)
		// 时不回填,与下方 line ~1201 "Redis 故障时 fail-open:不回填" 保持一致,
		// 避免在 Redis 异常期做一次注定失败的 SET。
		if s.cache != nil && cacheErr == nil {
			now := time.Now()
			startOfDay := timezone.StartOfDay(now)
			startOfWeek := timezone.StartOfWeek(now)
			sentinel := &UserPlatformQuotaCacheEntry{
				SchemaVersion:      UserPlatformQuotaCacheSchemaV1,
				DailyWindowStart:   &startOfDay,
				WeeklyWindowStart:  &startOfWeek,
				MonthlyWindowStart: &now,
				// limits 全 nil, usage 全 0(零值)
			}
			sentinelTTL := time.Duration(s.cfg.Billing.UserPlatformQuotaSentinelTTLSeconds) * time.Second
			if sentinelTTL <= 0 {
				// 防御:TTL<=0 时 Redis EXPIRE 会立即删除整个 key(见 billing_cache.go 的 pipe.Expire),
				// sentinel 不持久化 → 每请求击穿 DB。配置缺失/误配为 0 时 fallback 到 1h。
				sentinelTTL = time.Hour
			}
			setCtx, setCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			if setErr := s.cache.SetUserPlatformQuotaCache(setCtx, userID, platform, sentinel, sentinelTTL); setErr != nil {
				userPlatformQuotaSentinelSetCacheErrorTotal.Add(1)
				logger.LegacyPrintf("service.billing_cache", "Warning: set sentinel quota cache failed user=%d platform=%s: %v", userID, platform, setErr)
			}
			setCancel()
		}
		return nil, nil
	}

	now := time.Now()
	dailyUsage := rec.DailyUsageUSD
	weeklyUsage := rec.WeeklyUsageUSD
	monthlyUsage := rec.MonthlyUsageUSD
	if quotaWindowExpired(rec.DailyWindowStart, timezone.StartOfDay(now)) {
		dailyUsage = 0
	}
	if quotaWindowExpired(rec.WeeklyWindowStart, timezone.StartOfWeek(now)) {
		weeklyUsage = 0
	}
	if monthlyQuotaWindowExpired(rec.MonthlyWindowStart, now) {
		monthlyUsage = 0
	}

	// Redis 故障时 fail-open：不回填，直接用 DB 数据做一次性检查
	if cacheErr != nil {
		snapshot := &userPlatformQuotaSnapshot{
			dailyLimitUSD:      rec.DailyLimitUSD,
			weeklyLimitUSD:     rec.WeeklyLimitUSD,
			monthlyLimitUSD:    rec.MonthlyLimitUSD,
			dailyUsageUSD:      dailyUsage,
			weeklyUsageUSD:     weeklyUsage,
			monthlyUsageUSD:    monthlyUsage,
			dailyWindowStart:   rec.DailyWindowStart,
			weeklyWindowStart:  rec.WeeklyWindowStart,
			monthlyWindowStart: rec.MonthlyWindowStart,
		}
		return snapshot, snapshot.limitExceeded(0)
	}

	// cache MISS 或旧版 entry → 回填完整 entry（含 limits 和 window_start）
	newEntry := &UserPlatformQuotaCacheEntry{
		DailyUsageUSD:      dailyUsage,
		WeeklyUsageUSD:     weeklyUsage,
		MonthlyUsageUSD:    monthlyUsage,
		SchemaVersion:      UserPlatformQuotaCacheSchemaV1,
		DailyLimitUSD:      rec.DailyLimitUSD,
		WeeklyLimitUSD:     rec.WeeklyLimitUSD,
		MonthlyLimitUSD:    rec.MonthlyLimitUSD,
		DailyWindowStart:   rec.DailyWindowStart,
		WeeklyWindowStart:  rec.WeeklyWindowStart,
		MonthlyWindowStart: rec.MonthlyWindowStart,
	}
	if s.cache != nil {
		ttl := time.Duration(s.cfg.Billing.UserPlatformQuotaCacheTTLSeconds) * time.Second
		// 与 HIT 过期回填路径（上文 SetCache 调用）保持一致：用 context.Background()+50ms,
		// 避免请求 ctx 提前取消（客户端断连/上游超时）导致 cache 回填失败,
		// 让下一次 preflight 仍然 MISS 并击穿到 DB（高并发下增大 DB 压力）。
		setCtx, setCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		if setErr := s.cache.SetUserPlatformQuotaCache(setCtx, userID, platform, newEntry, ttl); setErr != nil {
			logger.LegacyPrintf("service.billing_cache", "Warning: set user platform quota cache failed user=%d platform=%s: %v", userID, platform, setErr)
		}
		setCancel()
	}

	snapshot := &userPlatformQuotaSnapshot{
		dailyLimitUSD:      rec.DailyLimitUSD,
		weeklyLimitUSD:     rec.WeeklyLimitUSD,
		monthlyLimitUSD:    rec.MonthlyLimitUSD,
		dailyUsageUSD:      dailyUsage,
		weeklyUsageUSD:     weeklyUsage,
		monthlyUsageUSD:    monthlyUsage,
		dailyWindowStart:   rec.DailyWindowStart,
		weeklyWindowStart:  rec.WeeklyWindowStart,
		monthlyWindowStart: rec.MonthlyWindowStart,
	}
	return snapshot, snapshot.limitExceeded(0)
}

// withWindowResetsMetadata 给 quota error 附加 window_resets_at metadata（RFC3339）。
func withWindowResetsMetadata(err error, resetAt time.Time) error {
	appErr, ok := err.(*infraerrors.ApplicationError)
	if !ok || appErr == nil {
		return err
	}
	return appErr.WithMetadata(map[string]string{
		"window_resets_at": resetAt.Format(time.RFC3339),
	})
}

// nextDailyReset 计算下一个日窗口起点（次日全局时区 0 点）。
// 必须与 timezone.StartOfDay 同口径，否则 Retry-After 会偏差。
func nextDailyReset(now time.Time) time.Time {
	return timezone.StartOfDay(now).AddDate(0, 0, 1)
}

// nextWeeklyReset 计算下一个周窗口起点（下周一全局时区 0 点）。
// 必须与 timezone.StartOfWeek 同口径，否则 Retry-After 会偏差。
func nextWeeklyReset(now time.Time) time.Time {
	return timezone.StartOfWeek(now).AddDate(0, 0, 7)
}

// nextMonthlyResetFrom 返回 30 天滚动窗口的下次重置时间（start + 30d）。
// start 为 nil（未初始化）或已过期（now-start >= 30d，与 monthlyQuotaWindowExpired 同口径）时
// 退化为 now+30d：过期窗口会在下次 increment 时重置为 now，下次重置即 now+30d；
// 否则按 start 计算会得到一个过去的时间，使 Retry-After 落回 fallback 并触发客户端紧凑重试。
func nextMonthlyResetFrom(start *time.Time, now time.Time) time.Time {
	if start == nil || now.Sub(*start) >= 30*24*time.Hour {
		return now.Add(30 * 24 * time.Hour)
	}
	return start.Add(30 * 24 * time.Hour)
}

// quotaWindowExpired 判断窗口是否已过期：start 为 nil（未初始化）或在 currWindowStart 之前视为已过期。
func quotaWindowExpired(start *time.Time, currWindowStart time.Time) bool {
	if start == nil {
		return true
	}
	return start.Before(currWindowStart)
}

// monthlyQuotaWindowExpired 判断 30 天滚动月度窗口是否已过期。
// 过期条件：now - start >= 30×24h（与订阅模式 NeedsMonthlyReset 语义一致）。
// start 为 nil 时视为已过期（未初始化窗口）。
func monthlyQuotaWindowExpired(start *time.Time, now time.Time) bool {
	if start == nil {
		return true
	}
	return now.Sub(*start) >= 30*24*time.Hour
}

// HasUserPlatformQuotaLimit 判断该 user×platform 是否设了任一非 nil limit。
// 写入点守卫:无 limit 直接跳过 Redis 写 + 脏集标记,消除无谓写入。
// fail-safe:任何不确定(simple 模式除外)都返回 true 维持写入。
func (s *BillingCacheService) HasUserPlatformQuotaLimit(ctx context.Context, userID int64, platform string) bool {
	if s.cfg.RunMode == config.RunModeSimple {
		return false
	}
	if s.cache == nil {
		return true
	}
	entry, ok, err := s.cache.GetUserPlatformQuotaCache(ctx, userID, platform)
	if err != nil || !ok || entry == nil {
		return true
	}
	return entry.DailyLimitUSD != nil || entry.WeeklyLimitUSD != nil || entry.MonthlyLimitUSD != nil
}
