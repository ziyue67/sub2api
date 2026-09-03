package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

// ConcurrencyCache 定义并发控制的缓存接口
// 使用有序集合存储槽位，按时间戳清理过期条目
type ConcurrencyCache interface {
	// 账号槽位管理
	// 键格式: concurrency:account:{accountID}（有序集合，成员为 requestID）
	AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error)
	ReleaseAccountSlot(ctx context.Context, accountID int64, requestID string) error
	GetAccountConcurrency(ctx context.Context, accountID int64) (int, error)
	GetAccountConcurrencyBatch(ctx context.Context, accountIDs []int64) (map[int64]int, error)

	// 账号等待队列（账号级）
	IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error)
	DecrementAccountWaitCount(ctx context.Context, accountID int64) error
	GetAccountWaitingCount(ctx context.Context, accountID int64) (int, error)

	// 用户槽位管理
	// 键格式: concurrency:user:{userID}（有序集合，成员为 requestID）
	AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error)
	ReleaseUserSlot(ctx context.Context, userID int64, requestID string) error
	GetUserConcurrency(ctx context.Context, userID int64) (int, error)

	// 等待队列计数（每次入队都会刷新 TTL，避免长时间排队时计数提前过期）
	IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error)
	DecrementWaitCount(ctx context.Context, userID int64) error

	// 批量负载查询（只读）
	GetAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error)
	GetUsersLoadBatch(ctx context.Context, users []UserWithConcurrency) (map[int64]*UserLoadInfo, error)

	// 清理过期槽位（后台任务）
	CleanupExpiredAccountSlots(ctx context.Context, accountID int64) error
	CleanupExpiredAccountSlotKeys(ctx context.Context) error

	// 启动时清理旧进程遗留槽位与等待计数
	CleanupStaleProcessSlots(ctx context.Context, activeRequestPrefix string) error
}

// LaneConcurrencyCache is the optional cache contract used by accounts that
// expose more than one independently schedulable egress lane.  It is kept
// separate from ConcurrencyCache deliberately: existing cache test doubles and
// older deployments can continue to implement the account/user contract while
// the scheduler feature-detects lane support with a type assertion.
//
// Lane slots use a lane-scoped Redis namespace (concurrency:lane:{laneID}).
// The service pairs that independent reservation with the parent account slot
// whenever an aggregate account limit is configured. The wait counter follows
// the lane namespace (wait:lane:{laneID}).
type LaneConcurrencyCache interface {
	AcquireLaneSlot(ctx context.Context, laneID int64, maxConcurrency int, requestID string) (bool, error)
	ReleaseLaneSlot(ctx context.Context, laneID int64, requestID string) error
	GetLaneConcurrency(ctx context.Context, laneID int64) (int, error)
	IncrementLaneWaitCount(ctx context.Context, laneID int64, maxWait int) (bool, error)
	DecrementLaneWaitCount(ctx context.Context, laneID int64) error
	GetLaneWaitingCount(ctx context.Context, laneID int64) (int, error)
}

// LaneConcurrencyBatchCache is an optional extension for admin/metrics paths
// that need to read several lane counters in one Redis pipeline.  Keeping it
// separate preserves the six-method LaneConcurrencyCache contract for small
// test doubles and third-party cache implementations.
type LaneConcurrencyBatchCache interface {
	LaneConcurrencyCache
	GetLaneConcurrencyBatch(ctx context.Context, laneIDs []int64) (map[int64]int, error)
}

type APIKeyConcurrencyCache interface {
	TrackAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error
	ReleaseAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error
	GetAPIKeyConcurrencyBatch(ctx context.Context, apiKeyIDs []int64) (map[int64]int, error)
}

// OpenAIWSIngressLeaseCache owns the short-lived distributed lease used to
// bound live client WebSocket sessions. It is deliberately independent of the
// request-slot namespace: idle ingress connections do not occupy turn slots.
type OpenAIWSIngressLeaseCache interface {
	AcquireOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, maxConnections int, leaseID string) (bool, error)
	RefreshOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, leaseID string) (bool, error)
	ReleaseOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, leaseID string) error
}

const (
	openAIWSIngressLeaseTTL             = 60 * time.Second
	openAIWSIngressLeaseRefreshInterval = 20 * time.Second
	openAIWSIngressLeaseOperationTO     = 2 * time.Second
)

var ErrOpenAIWSIngressLeaseLost = errors.New("openai websocket ingress lease lost")

// OpenAIWSIngressLease keeps a Redis-backed ingress lease alive and cancels
// its context if Redis cannot confirm ownership for a full lease lifetime.
// Call Release on every handler exit to reclaim capacity immediately.
type OpenAIWSIngressLease struct {
	ctx      context.Context
	cancel   context.CancelCauseFunc
	cache    OpenAIWSIngressLeaseCache
	apiKeyID int64
	leaseID  string

	stopOnce    sync.Once
	stopCh      chan struct{}
	refreshDone chan struct{}
}

func (l *OpenAIWSIngressLease) Context() context.Context {
	if l == nil || l.ctx == nil {
		return context.Background()
	}
	return l.ctx
}

func (l *OpenAIWSIngressLease) Release() {
	if l == nil {
		return
	}
	l.stopOnce.Do(func() {
		if l.stopCh != nil {
			close(l.stopCh)
		}
		if l.cancel != nil {
			l.cancel(nil)
		}
		if l.refreshDone != nil {
			<-l.refreshDone
		}
		if l.cache == nil || l.apiKeyID <= 0 || l.leaseID == "" {
			return
		}
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), openAIWSIngressLeaseOperationTO)
		defer releaseCancel()
		if err := l.cache.ReleaseOpenAIWSIngressLease(releaseCtx, l.apiKeyID, l.leaseID); err != nil {
			logger.L().Warn("openai_ws_ingress_lease_release_failed",
				zap.Int64("api_key_id", l.apiKeyID),
				zap.Error(err),
			)
		}
	})
}

func (l *OpenAIWSIngressLease) refreshLoop() {
	defer func() {
		if l != nil && l.refreshDone != nil {
			close(l.refreshDone)
		}
	}()
	if l == nil || l.cache == nil {
		return
	}
	ticker := time.NewTicker(openAIWSIngressLeaseRefreshInterval)
	defer ticker.Stop()
	lastConfirmedAt := time.Now()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-l.stopCh:
			return
		case <-ticker.C:
			var lost bool
			lastConfirmedAt, lost = l.refresh(lastConfirmedAt)
			if lost {
				l.cancel(ErrOpenAIWSIngressLeaseLost)
				return
			}
		}
	}
}

// refresh confirms the lease is still owned. A missing member is an immediate
// lease loss; transient Redis errors are tolerated only for one full lease TTL.
func (l *OpenAIWSIngressLease) refresh(lastConfirmedAt time.Time) (time.Time, bool) {
	refreshCtx, refreshCancel := context.WithTimeout(context.Background(), openAIWSIngressLeaseOperationTO)
	owned, err := l.cache.RefreshOpenAIWSIngressLease(refreshCtx, l.apiKeyID, l.leaseID)
	refreshCancel()
	if err == nil && owned {
		return time.Now(), false
	}
	if err == nil {
		err = ErrOpenAIWSIngressLeaseLost
	}
	elapsed := time.Since(lastConfirmedAt)
	logger.L().Warn("openai_ws_ingress_lease_refresh_failed",
		zap.Int64("api_key_id", l.apiKeyID),
		zap.Duration("unconfirmed_for", elapsed),
		zap.Error(err),
	)
	if errors.Is(err, ErrOpenAIWSIngressLeaseLost) || elapsed >= openAIWSIngressLeaseTTL {
		logger.L().Error("openai_ws_ingress_lease_lost",
			zap.Int64("api_key_id", l.apiKeyID),
			zap.Duration("unconfirmed_for", elapsed),
			zap.Error(err),
		)
		return lastConfirmedAt, true
	}
	return lastConfirmedAt, false
}

var (
	requestIDPrefix  = initRequestIDPrefix()
	requestIDCounter atomic.Uint64
)

func initRequestIDPrefix() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err == nil {
		return "r" + strconv.FormatUint(binary.BigEndian.Uint64(b), 36)
	}
	fallback := uint64(time.Now().UnixNano()) ^ (uint64(os.Getpid()) << 16)
	return "r" + strconv.FormatUint(fallback, 36)
}

func RequestIDPrefix() string {
	return requestIDPrefix
}

func generateRequestID() string {
	seq := requestIDCounter.Add(1)
	return requestIDPrefix + "-" + strconv.FormatUint(seq, 36)
}

func (s *ConcurrencyService) CleanupStaleProcessSlots(ctx context.Context) error {
	if s == nil || s.cache == nil {
		return nil
	}
	return s.cache.CleanupStaleProcessSlots(ctx, RequestIDPrefix())
}

const (
	// 默认等待队列额外槽位
	defaultExtraWaitSlots = 20

	defaultAccountLoadBatchCacheTTL = 200 * time.Millisecond
	accountLoadBatchFetchTimeout    = 3 * time.Second
	maxAccountLoadBatchCacheEntries = 256
	apiKeyConcurrencyFetchTimeout   = 3 * time.Second
	apiKeySlotTrackTimeout          = 2 * time.Second
)

// ConcurrencyService 管理账号和用户的并发限制。
type ConcurrencyService struct {
	cache ConcurrencyCache

	accountLoadCacheTTL atomic.Int64
	accountLoadCacheMu  sync.RWMutex
	accountLoadCache    map[string]cachedAccountLoadBatch
	accountLoadGroup    singleflight.Group
}

// LaneConcurrencySupported reports whether the configured concurrency cache
// implements the optional lane-scoped namespace.  Handlers use this capability
// probe when they need to choose between a lane slot and the legacy account
// slot during rolling upgrades.  Keep the nil handling in the service so
// callers do not need to reach into its private cache field.
func (s *ConcurrencyService) LaneConcurrencySupported() bool {
	return laneConcurrencySupported(s)
}

type cachedAccountLoadBatch struct {
	loadMap   map[int64]*AccountLoadInfo
	expiresAt time.Time
}

// NewConcurrencyService 创建并发控制服务。
func NewConcurrencyService(cache ConcurrencyCache) *ConcurrencyService {
	svc := &ConcurrencyService{
		cache:            cache,
		accountLoadCache: make(map[string]cachedAccountLoadBatch),
	}
	svc.SetAccountLoadBatchCacheTTL(defaultAccountLoadBatchCacheTTL)
	return svc
}

// AcquireOpenAIWSIngressLease atomically reserves one live ingress connection
// for an API key. A non-positive limit explicitly disables this protection.
func (s *ConcurrencyService) AcquireOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, maxConnections int) (*OpenAIWSIngressLease, bool, error) {
	if maxConnections <= 0 {
		return nil, true, nil
	}
	if s == nil || s.cache == nil || apiKeyID <= 0 {
		return nil, false, errors.New("openai websocket ingress lease cache is unavailable")
	}
	cache, ok := s.cache.(OpenAIWSIngressLeaseCache)
	if !ok {
		return nil, false, errors.New("openai websocket ingress lease cache is unsupported")
	}
	leaseID := generateRequestID()
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}
	acquireCtx, acquireCancel := context.WithTimeout(baseCtx, openAIWSIngressLeaseOperationTO)
	acquired, err := cache.AcquireOpenAIWSIngressLease(acquireCtx, apiKeyID, maxConnections, leaseID)
	acquireCancel()
	if err != nil || !acquired {
		return nil, acquired, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	leaseCtx, leaseCancel := context.WithCancelCause(ctx)
	lease := &OpenAIWSIngressLease{
		ctx:         leaseCtx,
		cancel:      leaseCancel,
		cache:       cache,
		apiKeyID:    apiKeyID,
		leaseID:     leaseID,
		stopCh:      make(chan struct{}),
		refreshDone: make(chan struct{}),
	}
	go lease.refreshLoop()
	return lease, true, nil
}

// SetAccountLoadBatchCacheTTL 设置账号负载批量读取的极短 TTL 缓存；非正数表示禁用缓存。
func (s *ConcurrencyService) SetAccountLoadBatchCacheTTL(ttl time.Duration) {
	if s == nil {
		return
	}
	s.accountLoadCacheTTL.Store(int64(ttl))
	if ttl <= 0 {
		s.accountLoadCacheMu.Lock()
		s.accountLoadCache = make(map[string]cachedAccountLoadBatch)
		s.accountLoadCacheMu.Unlock()
	}
}

// AcquireResult represents the result of acquiring a concurrency slot
type AcquireResult struct {
	Acquired    bool
	ReleaseFunc func() // Must be called when done (typically via defer)
	// MaxConcurrency is the exact limit supplied to the cache for this
	// reservation.  Keep it on the result because an Account may be projected
	// onto a proxy lane immediately after admission, changing Account.Concurrency
	// before the selection reaches a handler.
	//
	// MaxConcurrencySet distinguishes an explicit zero (unlimited) from a
	// hand-written/legacy AcquireResult produced by a test or third-party caller.
	MaxConcurrency    int
	MaxConcurrencySet bool
	// AggregateMaxConcurrency is populated when a lane request also reserves
	// the parent account bucket.  It lets callers retain both values after the
	// selected lane is projected onto Account.Concurrency.
	AggregateMaxConcurrency    int
	AggregateMaxConcurrencySet bool
	// Lane identifies the egress whose slot was acquired (or, on a failed
	// immediate acquire, the lane selected for the subsequent wait plan).
	// It is nil for legacy account-level slots.
	Lane *AccountProxyLane
}

type AccountWithConcurrency struct {
	ID             int64
	MaxConcurrency int
}

type UserWithConcurrency struct {
	ID             int64
	MaxConcurrency int
}

type AccountLoadInfo struct {
	AccountID          int64
	CurrentConcurrency int
	WaitingCount       int
	LoadRate           int // 0-100+ (percent)
}

type UserLoadInfo struct {
	UserID             int64
	CurrentConcurrency int
	WaitingCount       int
	LoadRate           int // 0-100+ (percent)
}

// AcquireAccountSlot attempts to acquire a concurrency slot for an account.
// If the account is at max concurrency, it waits until a slot is available or timeout.
// Returns a release function that MUST be called when the request completes.
func (s *ConcurrencyService) AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int) (*AcquireResult, error) {
	// A non-nil service with a nil cache is common in lightweight/admin
	// processes and during rolling startup. Treat it as the no-service path;
	// otherwise a positive account limit would dereference s.cache and panic.
	// If maxConcurrency is 0 or negative, no limit.
	if s == nil || s.cache == nil || maxConcurrency <= 0 {
		return &AcquireResult{
			Acquired:          true,
			ReleaseFunc:       func() {}, // no-op
			MaxConcurrency:    maxConcurrency,
			MaxConcurrencySet: true,
		}, nil
	}

	// Generate unique request ID for this slot
	requestID := generateRequestID()

	acquired, err := s.cache.AcquireAccountSlot(ctx, accountID, maxConcurrency, requestID)
	if err != nil {
		return nil, err
	}

	if acquired {
		return &AcquireResult{
			Acquired:          true,
			MaxConcurrency:    maxConcurrency,
			MaxConcurrencySet: true,
			ReleaseFunc: func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseAccountSlot(bgCtx, accountID, requestID); err != nil {
					logger.LegacyPrintf("service.concurrency", "Warning: failed to release account slot for %d (req=%s): %v", accountID, requestID, err)
				}
			},
		}, nil
	}

	return &AcquireResult{
		Acquired:          false,
		ReleaseFunc:       nil,
		MaxConcurrency:    maxConcurrency,
		MaxConcurrencySet: true,
	}, nil
}

// AcquireLaneSlot attempts to reserve one slot in a lane-specific bucket.
// Unlike AcquireAccountSlot this method is optional-capability aware: caches
// that predate lane scheduling fail open so rolling upgrades do not take down
// legacy traffic.  A concrete lane-capable cache (the Redis implementation)
// still enforces the configured limit atomically.
func (s *ConcurrencyService) AcquireLaneSlot(ctx context.Context, laneID int64, maxConcurrency int) (*AcquireResult, error) {
	if maxConcurrency <= 0 {
		return &AcquireResult{Acquired: true, ReleaseFunc: func() {}, MaxConcurrency: maxConcurrency, MaxConcurrencySet: true}, nil
	}
	if s == nil || s.cache == nil || laneID <= 0 {
		return &AcquireResult{Acquired: true, ReleaseFunc: func() {}, MaxConcurrency: maxConcurrency, MaxConcurrencySet: true}, nil
	}
	cache, ok := s.cache.(LaneConcurrencyCache)
	if !ok {
		// Optional interface: an old cache has no lane namespace.  Let the
		// caller fall back to its legacy account-level path rather than
		// returning an outage during a rolling deployment.
		return &AcquireResult{Acquired: true, ReleaseFunc: func() {}, MaxConcurrency: maxConcurrency, MaxConcurrencySet: true}, nil
	}

	requestID := generateRequestID()
	acquired, err := cache.AcquireLaneSlot(ctx, laneID, maxConcurrency, requestID)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return &AcquireResult{Acquired: false, MaxConcurrency: maxConcurrency, MaxConcurrencySet: true}, nil
	}

	return &AcquireResult{
		Acquired:          true,
		MaxConcurrency:    maxConcurrency,
		MaxConcurrencySet: true,
		ReleaseFunc: func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := cache.ReleaseLaneSlot(bgCtx, laneID, requestID); err != nil {
				logger.LegacyPrintf("service.concurrency", "Warning: failed to release lane slot for %d (req=%s): %v", laneID, requestID, err)
			}
		},
	}, nil
}

// AcquireAccountAndLaneSlot reserves both admission domains for one request:
// the parent account aggregate and the selected egress lane.  This is the
// concrete implementation of "total=20, IP1=10, IP2=10": no more than 20
// requests may use the account in total, and no more than 10 may use either
// lane.  The returned release function is a single lease for both slots.
//
// Older cache adapters do not expose lane namespaces.  In that case the
// account reservation remains authoritative and the lane portion is skipped,
// preserving rolling-upgrade compatibility without bypassing the aggregate
// limit.
func (s *ConcurrencyService) AcquireAccountAndLaneSlot(
	ctx context.Context,
	accountID int64,
	aggregateMaxConcurrency int,
	laneID int64,
	laneMaxConcurrency int,
) (*AcquireResult, error) {
	if laneID <= 0 || !s.LaneConcurrencySupported() {
		return s.AcquireAccountSlot(ctx, accountID, aggregateMaxConcurrency)
	}

	accountResult, err := s.AcquireAccountSlot(ctx, accountID, aggregateMaxConcurrency)
	if err != nil || accountResult == nil || !accountResult.Acquired {
		if accountResult == nil {
			return &AcquireResult{Acquired: false, MaxConcurrency: aggregateMaxConcurrency, MaxConcurrencySet: true}, err
		}
		accountResult.Lane = &AccountProxyLane{ID: laneID, Concurrency: laneMaxConcurrency}
		accountResult.AggregateMaxConcurrency = aggregateMaxConcurrency
		accountResult.AggregateMaxConcurrencySet = true
		accountResult.MaxConcurrency = laneMaxConcurrency
		return accountResult, err
	}

	laneResult, err := s.AcquireLaneSlot(ctx, laneID, laneMaxConcurrency)
	if err != nil {
		if accountResult.ReleaseFunc != nil {
			accountResult.ReleaseFunc()
		}
		return nil, err
	}
	if laneResult == nil || !laneResult.Acquired {
		if accountResult.ReleaseFunc != nil {
			accountResult.ReleaseFunc()
		}
		return &AcquireResult{
			Acquired:                   false,
			MaxConcurrency:             laneMaxConcurrency,
			MaxConcurrencySet:          true,
			AggregateMaxConcurrency:    aggregateMaxConcurrency,
			AggregateMaxConcurrencySet: true,
			Lane:                       &AccountProxyLane{ID: laneID, Concurrency: laneMaxConcurrency},
		}, nil
	}

	accountRelease := accountResult.ReleaseFunc
	laneRelease := laneResult.ReleaseFunc
	return &AcquireResult{
		Acquired:                   true,
		MaxConcurrency:             laneMaxConcurrency,
		MaxConcurrencySet:          true,
		AggregateMaxConcurrency:    aggregateMaxConcurrency,
		AggregateMaxConcurrencySet: true,
		Lane:                       &AccountProxyLane{ID: laneID, Concurrency: laneMaxConcurrency},
		ReleaseFunc: func() {
			if laneRelease != nil {
				laneRelease()
			}
			if accountRelease != nil {
				accountRelease()
			}
		},
	}, nil
}

// GetLaneConcurrency returns the currently occupied slots for one lane.  The
// lane contract is optional, so an old cache reports zero instead of breaking
// account selection while services are being rolled forward.
func (s *ConcurrencyService) GetLaneConcurrency(ctx context.Context, laneID int64) (int, error) {
	if s == nil || s.cache == nil || laneID <= 0 {
		return 0, nil
	}
	cache, ok := s.cache.(LaneConcurrencyCache)
	if !ok {
		return 0, nil
	}
	return cache.GetLaneConcurrency(ctx, laneID)
}

// GetLaneConcurrencyBatch reads lane counters when the optional cache exposes
// the batch extension.  A cache that only implements the core lane contract is
// still supported via a bounded per-lane fallback; this keeps callers simple
// while avoiding a hard dependency on the extension during rolling upgrades.
func (s *ConcurrencyService) GetLaneConcurrencyBatch(ctx context.Context, laneIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(laneIDs))
	for _, laneID := range laneIDs {
		if laneID > 0 {
			result[laneID] = 0
		}
	}
	if len(result) == 0 || s == nil || s.cache == nil {
		return result, nil
	}
	if batch, ok := s.cache.(LaneConcurrencyBatchCache); ok {
		baseCtx := context.Background()
		if ctx != nil {
			baseCtx = context.WithoutCancel(ctx)
		}
		redisCtx, cancel := context.WithTimeout(baseCtx, apiKeyConcurrencyFetchTimeout)
		defer cancel()
		counts, err := batch.GetLaneConcurrencyBatch(redisCtx, laneIDs)
		if err != nil {
			return result, err
		}
		for laneID := range result {
			result[laneID] = counts[laneID]
		}
		return result, nil
	}
	cache, ok := s.cache.(LaneConcurrencyCache)
	if !ok {
		return result, nil
	}
	for laneID := range result {
		count, err := cache.GetLaneConcurrency(ctx, laneID)
		if err != nil {
			return result, err
		}
		result[laneID] = count
	}
	return result, nil
}

// IncrementLaneWaitCount attempts to reserve one place in a lane's wait
// queue.  Wait queues are deliberately fail-open, matching account/user
// queues: a Redis outage must not turn a transient observability issue into a
// gateway-wide outage.
func (s *ConcurrencyService) IncrementLaneWaitCount(ctx context.Context, laneID int64, maxWait int) (bool, error) {
	if s == nil || s.cache == nil || laneID <= 0 {
		return true, nil
	}
	cache, ok := s.cache.(LaneConcurrencyCache)
	if !ok {
		return true, nil
	}
	allowed, err := cache.IncrementLaneWaitCount(ctx, laneID, maxWait)
	if err != nil {
		logger.LegacyPrintf("service.concurrency", "Warning: increment wait count failed for lane %d: %v", laneID, err)
		return true, nil
	}
	return allowed, nil
}

// DecrementLaneWaitCount releases one lane wait-queue entry.  It uses a
// detached context so cancellation of the HTTP request cannot leak queue
// depth indefinitely.
func (s *ConcurrencyService) DecrementLaneWaitCount(ctx context.Context, laneID int64) {
	if s == nil || s.cache == nil || laneID <= 0 {
		return
	}
	cache, ok := s.cache.(LaneConcurrencyCache)
	if !ok {
		return
	}
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}
	bgCtx, cancel := context.WithTimeout(baseCtx, 5*time.Second)
	defer cancel()
	if err := cache.DecrementLaneWaitCount(bgCtx, laneID); err != nil {
		logger.LegacyPrintf("service.concurrency", "Warning: decrement wait count failed for lane %d: %v", laneID, err)
	}
}

// GetLaneWaitingCount returns the current lane wait queue depth.  It is a
// best-effort read and returns zero when the optional capability is absent.
func (s *ConcurrencyService) GetLaneWaitingCount(ctx context.Context, laneID int64) (int, error) {
	if s == nil || s.cache == nil || laneID <= 0 {
		return 0, nil
	}
	cache, ok := s.cache.(LaneConcurrencyCache)
	if !ok {
		return 0, nil
	}
	return cache.GetLaneWaitingCount(ctx, laneID)
}

// AcquireUserSlot attempts to acquire a concurrency slot for a user.
// If the user is at max concurrency, it waits until a slot is available or timeout.
// Returns a release function that MUST be called when the request completes.
func (s *ConcurrencyService) AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int) (*AcquireResult, error) {
	// A service can be constructed before its cache is wired (for example in a
	// health/admin process). Fail open with a no-op lease instead of
	// dereferencing a nil cache for a positive limit.
	// If maxConcurrency is 0 or negative, no limit.
	if s == nil || s.cache == nil || maxConcurrency <= 0 {
		return &AcquireResult{
			Acquired:          true,
			ReleaseFunc:       func() {}, // no-op
			MaxConcurrency:    maxConcurrency,
			MaxConcurrencySet: true,
		}, nil
	}

	// Generate unique request ID for this slot
	requestID := generateRequestID()

	acquired, err := s.cache.AcquireUserSlot(ctx, userID, maxConcurrency, requestID)
	if err != nil {
		return nil, err
	}

	if acquired {
		return &AcquireResult{
			Acquired:          true,
			MaxConcurrency:    maxConcurrency,
			MaxConcurrencySet: true,
			ReleaseFunc: func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.cache.ReleaseUserSlot(bgCtx, userID, requestID); err != nil {
					logger.LegacyPrintf("service.concurrency", "Warning: failed to release user slot for %d (req=%s): %v", userID, requestID, err)
				}
			},
		}, nil
	}

	return &AcquireResult{
		Acquired:          false,
		ReleaseFunc:       nil,
		MaxConcurrency:    maxConcurrency,
		MaxConcurrencySet: true,
	}, nil
}

// TrackAPIKeySlot records one active request slot for an API key without
// applying key-level concurrency limits. It is fail-open: Redis errors are
// logged and return a no-op release function.
func (s *ConcurrencyService) TrackAPIKeySlot(ctx context.Context, apiKeyID int64) func() {
	if s == nil || s.cache == nil || apiKeyID <= 0 {
		return func() {}
	}
	cache, ok := s.cache.(APIKeyConcurrencyCache)
	if !ok {
		return func() {}
	}

	requestID := generateRequestID()
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}
	trackCtx, cancel := context.WithTimeout(baseCtx, apiKeySlotTrackTimeout)
	err := cache.TrackAPIKeySlot(trackCtx, apiKeyID, requestID)
	cancel()
	if err != nil {
		logger.LegacyPrintf("service.concurrency", "Warning: failed to track api key slot for %d (req=%s): %v", apiKeyID, requestID, err)
		return func() {}
	}

	return func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cache.ReleaseAPIKeySlot(bgCtx, apiKeyID, requestID); err != nil {
			logger.LegacyPrintf("service.concurrency", "Warning: failed to release api key slot for %d (req=%s): %v", apiKeyID, requestID, err)
		}
	}
}

// GetAPIKeyConcurrencyBatch gets real-time active request counts for API keys.
// Stats are best-effort: missing Redis support or Redis errors return zeroes.
func (s *ConcurrencyService) GetAPIKeyConcurrencyBatch(ctx context.Context, apiKeyIDs []int64) (map[int64]int, error) {
	result := zeroAPIKeyConcurrencyMap(apiKeyIDs)
	if len(apiKeyIDs) == 0 {
		return result, nil
	}
	if s == nil || s.cache == nil {
		return result, nil
	}
	cache, ok := s.cache.(APIKeyConcurrencyCache)
	if !ok {
		return result, nil
	}

	redisCtx, cancel := context.WithTimeout(context.Background(), apiKeyConcurrencyFetchTimeout)
	defer cancel()

	counts, err := cache.GetAPIKeyConcurrencyBatch(redisCtx, apiKeyIDs)
	if err != nil {
		logger.LegacyPrintf("service.concurrency", "Warning: get api key concurrency batch failed: %v", err)
		return result, nil
	}
	for _, apiKeyID := range apiKeyIDs {
		result[apiKeyID] = counts[apiKeyID]
	}
	return result, nil
}

func zeroAPIKeyConcurrencyMap(apiKeyIDs []int64) map[int64]int {
	result := make(map[int64]int, len(apiKeyIDs))
	for _, apiKeyID := range apiKeyIDs {
		result[apiKeyID] = 0
	}
	return result
}

// ============================================
// Wait Queue Count Methods
// ============================================

// IncrementWaitCount attempts to increment the wait queue counter for a user.
// Returns true if successful, false if the wait queue is full.
// maxWait should be user.Concurrency + defaultExtraWaitSlots
func (s *ConcurrencyService) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	if s.cache == nil {
		// Redis not available, allow request
		return true, nil
	}

	result, err := s.cache.IncrementWaitCount(ctx, userID, maxWait)
	if err != nil {
		// On error, allow the request to proceed (fail open)
		logger.LegacyPrintf("service.concurrency", "Warning: increment wait count failed for user %d: %v", userID, err)
		return true, nil
	}
	return result, nil
}

// DecrementWaitCount decrements the wait queue counter for a user.
// Should be called when a request completes or exits the wait queue.
func (s *ConcurrencyService) DecrementWaitCount(ctx context.Context, userID int64) {
	if s.cache == nil {
		return
	}

	// Use background context to ensure decrement even if original context is cancelled
	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.cache.DecrementWaitCount(bgCtx, userID); err != nil {
		logger.LegacyPrintf("service.concurrency", "Warning: decrement wait count failed for user %d: %v", userID, err)
	}
}

// IncrementAccountWaitCount increments the wait queue counter for an account.
func (s *ConcurrencyService) IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error) {
	if s.cache == nil {
		return true, nil
	}

	result, err := s.cache.IncrementAccountWaitCount(ctx, accountID, maxWait)
	if err != nil {
		logger.LegacyPrintf("service.concurrency", "Warning: increment wait count failed for account %d: %v", accountID, err)
		return true, nil
	}
	return result, nil
}

// DecrementAccountWaitCount decrements the wait queue counter for an account.
func (s *ConcurrencyService) DecrementAccountWaitCount(ctx context.Context, accountID int64) {
	if s.cache == nil {
		return
	}

	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.cache.DecrementAccountWaitCount(bgCtx, accountID); err != nil {
		logger.LegacyPrintf("service.concurrency", "Warning: decrement wait count failed for account %d: %v", accountID, err)
	}
}

// GetAccountWaitingCount gets current wait queue count for an account.
func (s *ConcurrencyService) GetAccountWaitingCount(ctx context.Context, accountID int64) (int, error) {
	if s.cache == nil {
		return 0, nil
	}
	return s.cache.GetAccountWaitingCount(ctx, accountID)
}

// CalculateMaxWait calculates the maximum wait queue size for a user
// maxWait = userConcurrency + defaultExtraWaitSlots
func CalculateMaxWait(userConcurrency int) int {
	if userConcurrency <= 0 {
		userConcurrency = 1
	}
	return userConcurrency + defaultExtraWaitSlots
}

// GetAccountsLoadBatch 批量获取账号负载信息。
func (s *ConcurrencyService) GetAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	return s.getAccountsLoadBatch(ctx, accounts, true)
}

// GetAccountsLoadBatchFresh 绕过极短 TTL 缓存，用于抢槽失败后的实时刷新兜底。
func (s *ConcurrencyService) GetAccountsLoadBatchFresh(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	return s.getAccountsLoadBatch(ctx, accounts, false)
}

func (s *ConcurrencyService) getAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency, allowCache bool) (map[int64]*AccountLoadInfo, error) {
	if len(accounts) == 0 {
		return map[int64]*AccountLoadInfo{}, nil
	}
	if s.cache == nil {
		return map[int64]*AccountLoadInfo{}, nil
	}

	ttl := time.Duration(s.accountLoadCacheTTL.Load())
	if !allowCache || ttl <= 0 {
		return s.fetchAccountsLoadBatch(ctx, accounts)
	}

	key := accountLoadBatchCacheKey(accounts)
	if cached, ok := s.getCachedAccountLoadBatch(key, time.Now()); ok {
		return cached, nil
	}

	value, err, _ := s.accountLoadGroup.Do(key, func() (any, error) {
		now := time.Now()
		if cached, ok := s.getCachedAccountLoadBatch(key, now); ok {
			return cached, nil
		}
		loadMap, fetchErr := s.fetchAccountsLoadBatch(ctx, accounts)
		if fetchErr != nil {
			return nil, fetchErr
		}
		cached := cloneAccountLoadMap(loadMap)
		s.storeCachedAccountLoadBatch(key, cached, now.Add(ttl))
		return cached, nil
	})
	if err != nil {
		return nil, err
	}
	loadMap, _ := value.(map[int64]*AccountLoadInfo)
	if loadMap == nil {
		return map[int64]*AccountLoadInfo{}, nil
	}
	return loadMap, nil
}

func (s *ConcurrencyService) fetchAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	if s.cache == nil {
		return map[int64]*AccountLoadInfo{}, nil
	}
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}
	redisCtx, cancel := context.WithTimeout(baseCtx, accountLoadBatchFetchTimeout)
	defer cancel()
	return s.cache.GetAccountsLoadBatch(redisCtx, accounts)
}

func (s *ConcurrencyService) getCachedAccountLoadBatch(key string, now time.Time) (map[int64]*AccountLoadInfo, bool) {
	s.accountLoadCacheMu.RLock()
	cached, ok := s.accountLoadCache[key]
	s.accountLoadCacheMu.RUnlock()
	if !ok {
		return nil, false
	}
	if !now.Before(cached.expiresAt) {
		s.accountLoadCacheMu.Lock()
		if current, exists := s.accountLoadCache[key]; exists && !now.Before(current.expiresAt) {
			delete(s.accountLoadCache, key)
		}
		s.accountLoadCacheMu.Unlock()
		return nil, false
	}
	return cached.loadMap, true
}

func (s *ConcurrencyService) storeCachedAccountLoadBatch(key string, loadMap map[int64]*AccountLoadInfo, expiresAt time.Time) {
	s.accountLoadCacheMu.Lock()
	if s.accountLoadCache == nil {
		s.accountLoadCache = make(map[string]cachedAccountLoadBatch)
	}
	if len(s.accountLoadCache) >= maxAccountLoadBatchCacheEntries {
		now := time.Now()
		for cacheKey, cached := range s.accountLoadCache {
			if !now.Before(cached.expiresAt) {
				delete(s.accountLoadCache, cacheKey)
			}
		}
		for len(s.accountLoadCache) >= maxAccountLoadBatchCacheEntries {
			for cacheKey := range s.accountLoadCache {
				delete(s.accountLoadCache, cacheKey)
				break
			}
		}
	}
	s.accountLoadCache[key] = cachedAccountLoadBatch{
		loadMap:   loadMap,
		expiresAt: expiresAt,
	}
	s.accountLoadCacheMu.Unlock()
}

func accountLoadBatchCacheKey(accounts []AccountWithConcurrency) string {
	hash := sha256.New()
	var buf [16]byte
	for _, account := range accounts {
		binary.LittleEndian.PutUint64(buf[:8], uint64(account.ID))
		binary.LittleEndian.PutUint64(buf[8:], uint64(int64(account.MaxConcurrency)))
		_, _ = hash.Write(buf[:])
	}
	sum := hash.Sum(nil)
	return strconv.Itoa(len(accounts)) + ":" + hex.EncodeToString(sum)
}

func cloneAccountLoadMap(loadMap map[int64]*AccountLoadInfo) map[int64]*AccountLoadInfo {
	if len(loadMap) == 0 {
		return map[int64]*AccountLoadInfo{}
	}
	clone := make(map[int64]*AccountLoadInfo, len(loadMap))
	for accountID, loadInfo := range loadMap {
		if loadInfo == nil {
			clone[accountID] = nil
			continue
		}
		copied := *loadInfo
		clone[accountID] = &copied
	}
	return clone
}

// GetUsersLoadBatch returns load info for multiple users.
func (s *ConcurrencyService) GetUsersLoadBatch(ctx context.Context, users []UserWithConcurrency) (map[int64]*UserLoadInfo, error) {
	if s.cache == nil {
		return map[int64]*UserLoadInfo{}, nil
	}
	return s.cache.GetUsersLoadBatch(ctx, users)
}

// CleanupExpiredAccountSlots removes expired slots for one account (background task).
func (s *ConcurrencyService) CleanupExpiredAccountSlots(ctx context.Context, accountID int64) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.CleanupExpiredAccountSlots(ctx, accountID)
}

// StartSlotCleanupWorker starts a background cleanup worker for expired account slots.
func (s *ConcurrencyService) StartSlotCleanupWorker(_ AccountRepository, interval time.Duration) {
	if s == nil || s.cache == nil || interval <= 0 {
		return
	}

	runCleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := s.cache.CleanupExpiredAccountSlotKeys(cleanupCtx)
		cancel()
		if err != nil {
			logger.LegacyPrintf("service.concurrency", "Warning: cleanup expired account slots failed: %v", err)
			return
		}
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		runCleanup()
		for range ticker.C {
			runCleanup()
		}
	}()
}

// GetAccountConcurrencyBatch gets current concurrency counts for multiple accounts.
// Uses a detached context with timeout to prevent HTTP request cancellation from
// causing the entire batch to fail (which would show all concurrency as 0).
func (s *ConcurrencyService) GetAccountConcurrencyBatch(ctx context.Context, accountIDs []int64) (map[int64]int, error) {
	if len(accountIDs) == 0 {
		return map[int64]int{}, nil
	}
	if s.cache == nil {
		result := make(map[int64]int, len(accountIDs))
		for _, accountID := range accountIDs {
			result[accountID] = 0
		}
		return result, nil
	}

	// Use a detached context so that a cancelled HTTP request doesn't cause
	// the Redis pipeline to fail and return all-zero concurrency counts.
	redisCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return s.cache.GetAccountConcurrencyBatch(redisCtx, accountIDs)
}
