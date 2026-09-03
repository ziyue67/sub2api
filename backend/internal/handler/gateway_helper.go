package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

const gatewayStreamHeartbeatBytesKey = "gateway_stream_heartbeat_bytes"

func recordGatewayStreamHeartbeat(c *gin.Context, written int) {
	if c == nil || written <= 0 {
		return
	}
	total, _ := c.Get(gatewayStreamHeartbeatBytesKey)
	bytes, _ := total.(int)
	c.Set(gatewayStreamHeartbeatBytesKey, bytes+written)
}

func gatewayStreamHasOnlyHeartbeats(c *gin.Context) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	value, ok := c.Get(gatewayStreamHeartbeatBytesKey)
	if !ok {
		return false
	}
	heartbeatBytes, _ := value.(int)
	return heartbeatBytes > 0 && c.Writer.Size() == heartbeatBytes
}

// claudeCodeValidator is a singleton validator for Claude Code client detection
var claudeCodeValidator = service.NewClaudeCodeValidator()

// SetClaudeCodeClientContext 检查请求是否来自 Claude Code 客户端，并设置到 context 中
// 返回更新后的 context
func SetClaudeCodeClientContext(c *gin.Context, body []byte, parsedReq *service.ParsedRequest) {
	if c == nil || c.Request == nil {
		return
	}
	ua := c.GetHeader("User-Agent")
	// Fast path：非 Claude CLI UA 直接判定 false，避免热路径二次 JSON 反序列化。
	if !claudeCodeValidator.ValidateUserAgent(ua) {
		ctx := service.SetClaudeCodeClient(c.Request.Context(), false)
		c.Request = c.Request.WithContext(ctx)
		return
	}

	isClaudeCode := false
	if !strings.Contains(c.Request.URL.Path, "messages") {
		// 与 Validate 行为一致：非 messages 路径 UA 命中即可视为 Claude Code 客户端。
		isClaudeCode = true
	} else {
		// 仅在确认为 Claude CLI 且 messages 路径时再做 body 解析。
		bodyMap := claudeCodeBodyMapFromParsedRequest(parsedReq)
		if bodyMap == nil && len(body) > 0 {
			_ = json.Unmarshal(body, &bodyMap)
		}
		isClaudeCode = claudeCodeValidator.Validate(c.Request, bodyMap)
	}

	// 更新 request context
	ctx := service.SetClaudeCodeClient(c.Request.Context(), isClaudeCode)

	// 仅在确认为 Claude Code 客户端时提取版本号写入 context
	if isClaudeCode {
		if version := claudeCodeValidator.ExtractVersion(ua); version != "" {
			ctx = service.SetClaudeCodeVersion(ctx, version)
		}
	}

	c.Request = c.Request.WithContext(ctx)
}

func claudeCodeBodyMapFromParsedRequest(parsedReq *service.ParsedRequest) map[string]any {
	if parsedReq == nil {
		return nil
	}
	bodyMap := map[string]any{
		"model": parsedReq.Model,
	}
	if parsedReq.HasSystem {
		if system, ok := parsedReq.SystemValue(); ok {
			bodyMap["system"] = system
		} else {
			bodyMap["system"] = nil
		}
	}
	if parsedReq.MetadataUserID != "" {
		bodyMap["metadata"] = map[string]any{"user_id": parsedReq.MetadataUserID}
	}
	return bodyMap
}

// 并发槽位等待相关常量
//
// 性能优化说明：
// 原实现使用固定间隔（100ms）轮询并发槽位，存在以下问题：
// 1. 高并发时频繁轮询增加 Redis 压力
// 2. 固定间隔可能导致多个请求同时重试（惊群效应）
//
// 新实现使用指数退避 + 抖动算法：
// 1. 初始退避 100ms，每次乘以 1.5，最大 2s
// 2. 添加 ±20% 的随机抖动，分散重试时间点
// 3. 减少 Redis 压力，避免惊群效应
const (
	// maxConcurrencyWait 等待并发槽位的最大时间
	maxConcurrencyWait = 30 * time.Second
	// defaultPingInterval 流式响应等待时发送 ping 的默认间隔
	defaultPingInterval = 10 * time.Second
	// initialBackoff 初始退避时间
	initialBackoff = 100 * time.Millisecond
	// backoffMultiplier 退避时间乘数（指数退避）
	backoffMultiplier = 1.5
	// maxBackoff 最大退避时间
	maxBackoff = 2 * time.Second
)

// SSEPingFormat defines the format of SSE ping events for different platforms
type SSEPingFormat string

const (
	// SSEPingFormatClaude is the Claude/Anthropic SSE ping format
	SSEPingFormatClaude SSEPingFormat = "data: {\"type\": \"ping\"}\n\n"
	// SSEPingFormatNone indicates no ping should be sent (e.g., OpenAI has no ping spec)
	SSEPingFormatNone SSEPingFormat = ""
	// SSEPingFormatComment is an SSE comment ping for OpenAI/Codex CLI clients
	SSEPingFormatComment SSEPingFormat = ":\n\n"
)

// ConcurrencyError represents a concurrency limit error with context
type ConcurrencyError struct {
	SlotType  string
	IsTimeout bool
}

func (e *ConcurrencyError) Error() string {
	if e.IsTimeout {
		return fmt.Sprintf("timeout waiting for %s concurrency slot", e.SlotType)
	}
	return fmt.Sprintf("%s concurrency limit reached", e.SlotType)
}

type WaitQueueFullError struct {
	SlotType string
}

func (e *WaitQueueFullError) Error() string {
	return "Too many pending requests, please retry later"
}

// ConcurrencyHelper provides common concurrency slot management for gateway handlers
type ConcurrencyHelper struct {
	concurrencyService *service.ConcurrencyService
	pingFormat         SSEPingFormat
	pingInterval       time.Duration
}

// NewConcurrencyHelper creates a new ConcurrencyHelper
func NewConcurrencyHelper(concurrencyService *service.ConcurrencyService, pingFormat SSEPingFormat, pingInterval time.Duration) *ConcurrencyHelper {
	if pingInterval <= 0 {
		pingInterval = defaultPingInterval
	}
	return &ConcurrencyHelper{
		concurrencyService: concurrencyService,
		pingFormat:         pingFormat,
		pingInterval:       pingInterval,
	}
}

// wrapReleaseOnDone ensures release runs at most once and still triggers on context cancellation.
// 用于避免客户端断开或上游超时导致的并发槽位泄漏。
// 优化：基于 context.AfterFunc 注册回调，避免每请求额外守护 goroutine。
func wrapReleaseOnDone(ctx context.Context, releaseFunc func()) func() {
	if releaseFunc == nil {
		return nil
	}
	var once sync.Once
	releaseOnce := func() {
		once.Do(releaseFunc)
	}
	stop := context.AfterFunc(ctx, releaseOnce)

	return func() {
		_ = stop()
		releaseOnce()
	}
}

// IncrementWaitCount increments the wait count for a user
func (h *ConcurrencyHelper) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	return h.concurrencyService.IncrementWaitCount(ctx, userID, maxWait)
}

// DecrementWaitCount decrements the wait count for a user
func (h *ConcurrencyHelper) DecrementWaitCount(ctx context.Context, userID int64) {
	h.concurrencyService.DecrementWaitCount(ctx, userID)
}

// IncrementAccountWaitCount increments the wait count for an account
func (h *ConcurrencyHelper) IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error) {
	return h.concurrencyService.IncrementAccountWaitCount(ctx, accountID, maxWait)
}

// DecrementAccountWaitCount decrements the wait count for an account
func (h *ConcurrencyHelper) DecrementAccountWaitCount(ctx context.Context, accountID int64) {
	h.concurrencyService.DecrementAccountWaitCount(ctx, accountID)
}

// IncrementLaneWaitCount increments the wait count for one account egress
// lane. Lane-enabled accounts must not share the aggregate account queue: a
// saturated IP/transport should only back-pressure requests assigned to that
// lane. The service keeps this capability optional so old cache adapters remain
// compatible during a rolling upgrade.
func (h *ConcurrencyHelper) IncrementLaneWaitCount(ctx context.Context, laneID int64, maxWait int) (bool, error) {
	if h == nil || h.concurrencyService == nil {
		return true, nil
	}
	return h.concurrencyService.IncrementLaneWaitCount(ctx, laneID, maxWait)
}

// DecrementLaneWaitCount releases one entry from a lane wait queue.
func (h *ConcurrencyHelper) DecrementLaneWaitCount(ctx context.Context, laneID int64) {
	if h == nil || h.concurrencyService == nil {
		return
	}
	h.concurrencyService.DecrementLaneWaitCount(ctx, laneID)
}

// IncrementAccountOrLaneWaitCount dispatches a wait-queue reservation based on
// the selection plan. A zero lane ID is the legacy account namespace.
func (h *ConcurrencyHelper) IncrementAccountOrLaneWaitCount(ctx context.Context, accountID, laneID int64, maxWait int) (bool, error) {
	if laneID > 0 {
		return h.IncrementLaneWaitCount(ctx, laneID, maxWait)
	}
	return h.IncrementAccountWaitCount(ctx, accountID, maxWait)
}

// DecrementAccountOrLaneWaitCount mirrors IncrementAccountOrLaneWaitCount and
// is safe to call after a failed/partial wait reservation.
func (h *ConcurrencyHelper) DecrementAccountOrLaneWaitCount(ctx context.Context, accountID, laneID int64) {
	if laneID > 0 {
		h.DecrementLaneWaitCount(ctx, laneID)
		return
	}
	h.DecrementAccountWaitCount(ctx, accountID)
}

// TryAcquireUserSlot 尝试立即获取用户并发槽位。
// 返回值: (releaseFunc, acquired, error)
func (h *ConcurrencyHelper) TryAcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int) (func(), bool, error) {
	result, err := h.concurrencyService.AcquireUserSlot(ctx, userID, maxConcurrency)
	if err != nil {
		return nil, false, err
	}
	if !result.Acquired {
		return nil, false, nil
	}
	return result.ReleaseFunc, true, nil
}

func (h *ConcurrencyHelper) TryAcquireUserSlotForAPIKey(ctx context.Context, userID int64, maxConcurrency int, apiKeyID int64) (func(), bool, error) {
	releaseFunc, acquired, err := h.TryAcquireUserSlot(ctx, userID, maxConcurrency)
	if err != nil || !acquired {
		return releaseFunc, acquired, err
	}
	return h.withAPIKeySlot(ctx, apiKeyID, releaseFunc), true, nil
}

// AcquireOpenAIWSIngressLease bounds the whole client WebSocket lifecycle,
// independently from per-turn user and account slots.
func (h *ConcurrencyHelper) AcquireOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, maxConnections int) (*service.OpenAIWSIngressLease, bool, error) {
	if h == nil || h.concurrencyService == nil {
		return nil, false, fmt.Errorf("concurrency service is unavailable")
	}
	return h.concurrencyService.AcquireOpenAIWSIngressLease(ctx, apiKeyID, maxConnections)
}

// TryAcquireAccountSlot 尝试立即获取账号并发槽位。
// 返回值: (releaseFunc, acquired, error)
func (h *ConcurrencyHelper) TryAcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int) (func(), bool, error) {
	result, err := h.concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)
	if err != nil {
		return nil, false, err
	}
	if !result.Acquired {
		return nil, false, nil
	}
	return result.ReleaseFunc, true, nil
}

// TryAcquireLaneSlot attempts an immediate slot acquisition in a specific
// egress lane. It is intentionally separate from TryAcquireAccountSlot so
// legacy callers keep their account-level semantics.
func (h *ConcurrencyHelper) TryAcquireLaneSlot(ctx context.Context, laneID int64, maxConcurrency int) (func(), bool, error) {
	if h == nil || h.concurrencyService == nil {
		return func() {}, true, nil
	}
	result, err := h.concurrencyService.AcquireLaneSlot(ctx, laneID, maxConcurrency)
	if err != nil {
		return nil, false, err
	}
	if result == nil || !result.Acquired {
		return nil, false, nil
	}
	return result.ReleaseFunc, true, nil
}

// TryAcquireAccountOrLaneSlot is the immediate counterpart of the wait-plan
// dispatcher. It is used by handlers that perform a second fast-path attempt
// after the scheduler returned a non-acquired WaitPlan.
func (h *ConcurrencyHelper) TryAcquireAccountOrLaneSlot(ctx context.Context, accountID, laneID int64, maxConcurrency int, aggregateMaxConcurrency ...int) (func(), bool, error) {
	if laneID > 0 {
		if len(aggregateMaxConcurrency) > 0 && h != nil && h.concurrencyService != nil {
			result, err := h.concurrencyService.AcquireAccountAndLaneSlot(ctx, accountID, aggregateMaxConcurrency[0], laneID, maxConcurrency)
			if err != nil {
				return nil, false, err
			}
			if result == nil || !result.Acquired {
				return nil, false, nil
			}
			return result.ReleaseFunc, true, nil
		}
		return h.TryAcquireLaneSlot(ctx, laneID, maxConcurrency)
	}
	return h.TryAcquireAccountSlot(ctx, accountID, maxConcurrency)
}

// openAIWSLaneIDForAccount returns a lane ID only when the running cache
// supports the optional lane-scoped concurrency namespace.  Older cache
// adapters intentionally fail open in AcquireLaneSlot; passing a lane ID to
// them would therefore bypass the legacy account slot on WebSocket turns.
// Keep the capability check at this boundary so every WS re-acquire path uses
// the same admission namespace during a rolling upgrade.
func openAIWSLaneIDForAccount(account *service.Account, concurrency *service.ConcurrencyService) int64 {
	if account == nil || account.SelectedProxyLane == nil || concurrency == nil || !concurrency.LaneConcurrencySupported() {
		return 0
	}
	if account.SelectedProxyLane.ID <= 0 {
		return 0
	}
	return account.SelectedProxyLane.ID
}

// openAIWSLaneIDForHandler safely obtains the capability-aware lane ID from a
// gateway handler.  A partially constructed handler is common in focused unit
// tests and must retain the legacy account namespace rather than panic.
func (h *OpenAIGatewayHandler) openAIWSLaneIDForHandler(account *service.Account) int64 {
	if h == nil || h.concurrencyHelper == nil {
		return 0
	}
	return openAIWSLaneIDForAccount(account, h.concurrencyHelper.concurrencyService)
}

// openAIWSAccountMaxConcurrencyForSelection returns the concurrency limit for
// the admission namespace that owns the current WebSocket connection.
//
// A WaitPlan is authoritative when one exists, including MaxConcurrency == 0:
// zero means "unlimited" and is a valid limit, not an indication that the
// plan is incomplete.  During a rolling upgrade an account with proxy lanes
// still uses the legacy account Redis bucket (LaneID == 0); in that case the
// lane projection on Account may contain the lane's own cap, so consulting
// account.Concurrency would accidentally tighten an unlimited/aggregate
// account to that lane cap.  Lane-capable plans may refresh their lane cap
// after a terminal account hydration; use the current selected lane when it
// still owns the plan so subsequent turns do not keep a stale limit.
func (h *OpenAIGatewayHandler) openAIWSAccountMaxConcurrencyForSelection(
	account *service.Account,
	selection *service.AccountSelectionResult,
) int {
	if selection != nil && selection.WaitPlan != nil {
		plan := selection.WaitPlan
		if plan.LaneID > 0 && account != nil && account.SelectedProxyLane != nil &&
			account.SelectedProxyLane.ID == plan.LaneID && h != nil && h.concurrencyHelper != nil &&
			h.concurrencyHelper.concurrencyService != nil && h.concurrencyHelper.concurrencyService.LaneConcurrencySupported() {
			return account.SelectedProxyLane.Concurrency
		}
		// A lane wait plan carries MaxConcurrency for the selected lane.  Do not
		// fall through to AdmissionMaxConcurrency here: for composite admission
		// that metadata intentionally stores the parent aggregate (for example
		// total=20 while this lane is capped at 10).  Losing the lane cap on a
		// stale/missing request-local projection would let one egress exceed its
		// own limit during a WebSocket retry.
		if plan.LaneID > 0 {
			return plan.MaxConcurrency
		}
		if selection.AdmissionMaxConcurrencySet {
			return selection.AdmissionMaxConcurrency
		}
		// Preserve zero (unlimited) and every other value exactly as supplied by
		// the scheduler.  A legacy account-level plan deliberately has LaneID=0.
		return plan.MaxConcurrency
	}
	if selection != nil && selection.AdmissionMaxConcurrencySet {
		// Acquired lane selections have no WaitPlan, but their admission
		// metadata is a snapshot from the first turn.  A long-lived WS turn can
		// refresh the same lane's concurrency cap; when the running cache owns
		// the lane namespace, the current selected lane is authoritative and must
		// supersede that stale snapshot.  Legacy account-namespace selections
		// intentionally keep the explicit aggregate metadata (including zero).
		if account != nil && account.SelectedProxyLane != nil && h != nil && h.concurrencyHelper != nil &&
			h.concurrencyHelper.concurrencyService != nil && h.concurrencyHelper.concurrencyService.LaneConcurrencySupported() {
			return account.SelectedProxyLane.Concurrency
		}
		return selection.AdmissionMaxConcurrency
	}
	if account == nil {
		return 0
	}
	return account.Concurrency
}

// openAIWSAccountAggregateMaxConcurrencyArgs preserves the distinction
// between an explicit aggregate value (including zero = unlimited) and an
// older selection that carried no aggregate metadata. Callers pass the
// returned slice to the variadic composite-acquire helper; an empty slice
// intentionally keeps the legacy lane-only path for such old selections.
func (h *OpenAIGatewayHandler) openAIWSAccountAggregateMaxConcurrencyArgs(
	account *service.Account,
	selection *service.AccountSelectionResult,
) []int {
	if selection == nil {
		return nil
	}
	if plan := selection.WaitPlan; plan != nil {
		if plan.LaneID <= 0 || !plan.AggregateMaxConcurrencySet {
			return nil
		}
		return []int{plan.AggregateMaxConcurrency}
	}
	if !selection.AdmissionMaxConcurrencySet || h == nil || h.openAIWSLaneIDForHandler(account) <= 0 {
		return nil
	}
	return []int{selection.AdmissionMaxConcurrency}
}

// AcquireUserSlotWithWait acquires a user concurrency slot, waiting if necessary.
// For streaming requests, sends ping events during the wait.
// streamStarted is updated if streaming response has begun.
func (h *ConcurrencyHelper) AcquireUserSlotWithWait(c *gin.Context, userID int64, maxConcurrency int, isStream bool, streamStarted *bool) (func(), error) {
	return h.acquireUserSlotWithWaitTimeout(c, userID, maxConcurrency, maxConcurrencyWait, isStream, streamStarted)
}

func (h *ConcurrencyHelper) acquireUserSlotWithWaitTimeout(c *gin.Context, userID int64, maxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool) (func(), error) {
	ctx := c.Request.Context()

	// Try to acquire immediately
	releaseFunc, acquired, err := h.TryAcquireUserSlot(ctx, userID, maxConcurrency)
	if err != nil {
		return nil, err
	}

	if acquired {
		return h.withAPIKeySlotFromGin(c, releaseFunc), nil
	}

	queueLimit := service.CalculateMaxWait(maxConcurrency) - maxConcurrency
	if queueLimit < 1 {
		queueLimit = 1
	}
	canWait, err := h.IncrementWaitCount(ctx, userID, queueLimit)
	if err != nil {
		return nil, err
	}
	if !canWait {
		return nil, &WaitQueueFullError{SlotType: "user"}
	}
	defer h.DecrementWaitCount(ctx, userID)

	// Need to wait - handle streaming ping if needed
	releaseFunc, err = h.waitForSlotWithPingTimeout(c, "user", userID, maxConcurrency, timeout, isStream, streamStarted, false)
	if err != nil {
		return nil, err
	}
	return h.withAPIKeySlotFromGin(c, releaseFunc), nil
}

func (h *ConcurrencyHelper) withAPIKeySlotFromGin(c *gin.Context, releaseFunc func()) func() {
	if c == nil {
		return releaseFunc
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		return releaseFunc
	}
	return h.withAPIKeySlot(c.Request.Context(), apiKey.ID, releaseFunc)
}

func (h *ConcurrencyHelper) withAPIKeySlot(ctx context.Context, apiKeyID int64, releaseFunc func()) func() {
	if h == nil || h.concurrencyService == nil || apiKeyID <= 0 {
		return releaseFunc
	}
	apiKeyReleaseFunc := h.concurrencyService.TrackAPIKeySlot(ctx, apiKeyID)
	return func() {
		if releaseFunc != nil {
			releaseFunc()
		}
		if apiKeyReleaseFunc != nil {
			apiKeyReleaseFunc()
		}
	}
}

// AcquireAccountSlotWithWait acquires an account concurrency slot, waiting if necessary.
// For streaming requests, sends ping events during the wait.
// streamStarted is updated if streaming response has begun.
func (h *ConcurrencyHelper) AcquireAccountSlotWithWait(c *gin.Context, accountID int64, maxConcurrency int, isStream bool, streamStarted *bool) (func(), error) {
	ctx := c.Request.Context()

	// Try to acquire immediately
	releaseFunc, acquired, err := h.TryAcquireAccountSlot(ctx, accountID, maxConcurrency)
	if err != nil {
		return nil, err
	}

	if acquired {
		return releaseFunc, nil
	}

	// Need to wait - handle streaming ping if needed
	return h.waitForSlotWithPing(c, "account", accountID, maxConcurrency, isStream, streamStarted)
}

// waitForSlotWithPing waits for a concurrency slot, sending ping events for streaming requests.
// streamStarted pointer is updated when streaming begins (for proper error handling by caller).
func (h *ConcurrencyHelper) waitForSlotWithPing(c *gin.Context, slotType string, id int64, maxConcurrency int, isStream bool, streamStarted *bool) (func(), error) {
	return h.waitForSlotWithPingTimeout(c, slotType, id, maxConcurrency, maxConcurrencyWait, isStream, streamStarted, false)
}

// waitForSlotWithPingTimeout waits for a concurrency slot with a custom timeout.
func (h *ConcurrencyHelper) waitForSlotWithPingTimeout(c *gin.Context, slotType string, id int64, maxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool, tryImmediate bool) (func(), error) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	acquireSlot := func() (*service.AcquireResult, error) {
		if slotType == "user" {
			return h.concurrencyService.AcquireUserSlot(ctx, id, maxConcurrency)
		}
		if slotType == "lane" {
			return h.concurrencyService.AcquireLaneSlot(ctx, id, maxConcurrency)
		}
		return h.concurrencyService.AcquireAccountSlot(ctx, id, maxConcurrency)
	}

	if tryImmediate {
		result, err := acquireSlot()
		if err != nil {
			return nil, err
		}
		if result.Acquired {
			return result.ReleaseFunc, nil
		}
	}

	// Determine if ping is needed (streaming + ping format defined)
	needPing := isStream && h.pingFormat != ""

	var flusher http.Flusher
	if needPing {
		var ok bool
		flusher, ok = c.Writer.(http.Flusher)
		if !ok {
			return nil, fmt.Errorf("streaming not supported")
		}
	}

	// Only create ping ticker if ping is needed
	var pingCh <-chan time.Time
	if needPing {
		pingTicker := time.NewTicker(h.pingInterval)
		defer pingTicker.Stop()
		pingCh = pingTicker.C
	}

	backoff := initialBackoff
	timer := time.NewTimer(backoff)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			if parentErr := c.Request.Context().Err(); parentErr != nil {
				return nil, parentErr
			}
			return nil, &ConcurrencyError{
				SlotType:  slotType,
				IsTimeout: true,
			}

		case <-pingCh:
			// Send ping to keep connection alive
			if !*streamStarted {
				c.Header("Content-Type", "text/event-stream")
				c.Header("Cache-Control", "no-cache")
				c.Header("Connection", "keep-alive")
				c.Header("X-Accel-Buffering", "no")
				*streamStarted = true
			}
			written, err := fmt.Fprint(c.Writer, string(h.pingFormat))
			if err != nil {
				return nil, err
			}
			recordGatewayStreamHeartbeat(c, written)
			flusher.Flush()

		case <-timer.C:
			// Try to acquire slot
			result, err := acquireSlot()
			if err != nil {
				return nil, err
			}

			if result.Acquired {
				return result.ReleaseFunc, nil
			}
			backoff = nextBackoff(backoff)
			timer.Reset(backoff)
		}
	}
}

// AcquireAccountSlotWithWaitTimeout acquires an account slot with a custom timeout (keeps SSE ping).
func (h *ConcurrencyHelper) AcquireAccountSlotWithWaitTimeout(c *gin.Context, accountID int64, maxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool) (func(), error) {
	return h.waitForSlotWithPingTimeout(c, "account", accountID, maxConcurrency, timeout, isStream, streamStarted, true)
}

// AcquireLaneSlotWithWaitTimeout waits for a slot in one egress lane while
// preserving the same heartbeat/cancellation/backoff behavior as the legacy
// account helper. A lane wait is paired with the aggregate account slot when
// the caller supplies the account-wide limit; without it, the lane namespace
// remains independently addressable for rolling-upgrade compatibility.
func (h *ConcurrencyHelper) AcquireLaneSlotWithWaitTimeout(c *gin.Context, laneID int64, maxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool) (func(), error) {
	return h.waitForSlotWithPingTimeout(c, "lane", laneID, maxConcurrency, timeout, isStream, streamStarted, true)
}

// AcquireAccountOrLaneSlotWithWaitTimeout is the common handler entry point
// for AccountWaitPlan. It keeps the old account behavior when LaneID is zero
// and routes lane-enabled plans to the independent lane namespace.
func (h *ConcurrencyHelper) AcquireAccountOrLaneSlotWithWaitTimeout(c *gin.Context, accountID, laneID int64, maxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool, aggregateMaxConcurrency ...int) (func(), error) {
	if laneID > 0 {
		if len(aggregateMaxConcurrency) > 0 {
			return h.acquireAccountAndLaneSlotWithWaitTimeout(c, accountID, laneID, aggregateMaxConcurrency[0], maxConcurrency, timeout, isStream, streamStarted)
		}
		return h.AcquireLaneSlotWithWaitTimeout(c, laneID, maxConcurrency, timeout, isStream, streamStarted)
	}
	return h.AcquireAccountSlotWithWaitTimeout(c, accountID, maxConcurrency, timeout, isStream, streamStarted)
}

// acquireAccountAndLaneSlotWithWaitTimeout waits until both the account-wide
// aggregate slot and the selected lane slot are available.  It deliberately
// releases the first reservation when the second is full, so a request never
// holds total capacity while waiting for an egress-specific capacity unit.
func (h *ConcurrencyHelper) acquireAccountAndLaneSlotWithWaitTimeout(c *gin.Context, accountID, laneID int64, aggregateMaxConcurrency, laneMaxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool) (func(), error) {
	if h == nil || h.concurrencyService == nil {
		return func() {}, nil
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	acquire := func() (*service.AcquireResult, error) {
		return h.concurrencyService.AcquireAccountAndLaneSlot(ctx, accountID, aggregateMaxConcurrency, laneID, laneMaxConcurrency)
	}

	result, err := acquire()
	if err != nil {
		return nil, err
	}
	if result != nil && result.Acquired {
		return result.ReleaseFunc, nil
	}

	needPing := isStream && h.pingFormat != ""
	var flusher http.Flusher
	if needPing {
		var ok bool
		flusher, ok = c.Writer.(http.Flusher)
		if !ok {
			return nil, fmt.Errorf("streaming not supported")
		}
	}
	var pingCh <-chan time.Time
	if needPing {
		pingTicker := time.NewTicker(h.pingInterval)
		defer pingTicker.Stop()
		pingCh = pingTicker.C
	}
	backoff := initialBackoff
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			if parentErr := c.Request.Context().Err(); parentErr != nil {
				return nil, parentErr
			}
			return nil, &ConcurrencyError{SlotType: "account", IsTimeout: true}
		case <-pingCh:
			if !*streamStarted {
				c.Header("Content-Type", "text/event-stream")
				c.Header("Cache-Control", "no-cache")
				c.Header("Connection", "keep-alive")
				c.Header("X-Accel-Buffering", "no")
				*streamStarted = true
			}
			written, writeErr := fmt.Fprint(c.Writer, string(h.pingFormat))
			if writeErr != nil {
				return nil, writeErr
			}
			recordGatewayStreamHeartbeat(c, written)
			flusher.Flush()
		case <-timer.C:
			result, err = acquire()
			if err != nil {
				return nil, err
			}
			if result != nil && result.Acquired {
				return result.ReleaseFunc, nil
			}
			backoff = nextBackoff(backoff)
			timer.Reset(backoff)
		}
	}
}

func waitPlanAggregateMaxArgs(plan *service.AccountWaitPlan) []int {
	if plan == nil || plan.LaneID <= 0 || !plan.AggregateMaxConcurrencySet {
		return nil
	}
	return []int{plan.AggregateMaxConcurrency}
}

// nextBackoff 计算下一次退避时间
// 性能优化：使用指数退避 + 随机抖动，避免惊群效应
// current: 当前退避时间
// 返回值：下一次退避时间（100ms ~ 2s 之间）
func nextBackoff(current time.Duration) time.Duration {
	// 指数退避：当前时间 * 1.5
	next := time.Duration(float64(current) * backoffMultiplier)
	if next > maxBackoff {
		next = maxBackoff
	}
	// 添加 ±20% 的随机抖动（jitter 范围 0.8 ~ 1.2）
	// 抖动可以分散多个请求的重试时间点，避免同时冲击 Redis
	jitter := 0.8 + rand.Float64()*0.4
	jittered := time.Duration(float64(next) * jitter)
	if jittered < initialBackoff {
		return initialBackoff
	}
	if jittered > maxBackoff {
		return maxBackoff
	}
	return jittered
}
