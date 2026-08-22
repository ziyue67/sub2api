package service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// codexFingerprintIDsContextKey 是暂存在 gin context 的收敛 ID 集合键。
// 由 Forward（非透传）或 forwardOpenAIPassthrough（透传）解析后写入，请求
// 构造器读取用于出站头改写——请求体与出站头必须共享同一份 IDs，保证
// turn_id 等随机字段一致。
const codexFingerprintIDsContextKey = "codex_fingerprint_ids"
const codexClientIdentifiersContextKey = "codex_client_identifiers"

// stageCodexFingerprintIDs 将本 attempt 解析出的收敛 ID 暂存到 gin context。
// 必须无条件覆写（含 nil）：failover 从收敛账号切到 off 账号时，上一账号的
// IDs 不得残留并被误应用到新账号的出站头（typed-nil 由应用侧 nil 守卫吸收）。
func stageCodexFingerprintIDs(c *gin.Context, ids *codexFingerprintIDs) {
	if c != nil {
		c.Set(codexFingerprintIDsContextKey, ids)
	}
}

func stagedCodexFingerprintIDs(c *gin.Context, account *Account) *codexFingerprintIDs {
	if c == nil || account == nil || account.Type != AccountTypeOAuth {
		return nil
	}
	value, ok := c.Get(codexFingerprintIDsContextKey)
	if !ok {
		return nil
	}
	ids, ok := value.(*codexFingerprintIDs)
	if !ok || ids == nil || ids.accountID != account.ID {
		return nil
	}
	return ids
}

// applyStagedCodexFingerprintHeaders 读取 context 暂存的收敛 ID 并改写出站头。
// 非透传与透传两个请求构造器共用本函数，防止应用语义漂移。仅解析该
// snapshot 的 OAuth 账号可读取，避免 stale context 跨账号 failover 泄漏。
func applyStagedCodexFingerprintHeaders(c *gin.Context, account *Account, h http.Header) {
	applyCodexFingerprintHeaders(h, stagedCodexFingerprintIDs(c, account))
}

func applyStagedCodexFingerprintClientMetadata(c *gin.Context, account *Account, reqBody map[string]any) bool {
	return applyCodexFingerprintClientMetadata(reqBody, stagedCodexFingerprintIDs(c, account))
}

// codexFingerprintMode 控制 OAuth 账号出站请求的设备指纹收敛强度。
// 多人共享同一 OAuth 账号时，每个用户的 Codex 客户端会携带各自不同的
// installation_id / session_id / thread_id，上游据此判定设备数和会话数。
// 收敛模式将这些标识改写为账号级恒定值，减少上游可见的设备/会话指纹。
type codexFingerprintMode string

const (
	// codexFingerprintOff 不做任何收敛，原样透传客户端标识。
	// 这是默认值：收敛是显式 opt-in 的（见 GetCodexFingerprintMode）。
	codexFingerprintOff codexFingerprintMode = "off"
	// codexFingerprintDevice 仅收敛 installation_id 为账号级恒定值。
	// 上游看到 1 台设备 + 多会话（每用户各自的 session）。
	codexFingerprintDevice codexFingerprintMode = "device"
	// codexFingerprintSession 收敛 installation_id + session_id，
	// thread_id 按客户端原始 session-id 确定性派生（每个真实 Codex 会话一个独立线程）。
	// 上游看到 1 台设备 + 1 会话 + N 线程，最接近正常用户 spawn 子代理的模式。
	codexFingerprintSession codexFingerprintMode = "session"
	// codexFingerprintFull 收敛所有标识：installation_id + session_id + thread_id。
	// 上游看到 1 台设备 + 1 会话 + 1 线程，最激进。
	codexFingerprintFull codexFingerprintMode = "full"
)

const (
	codexFingerprintModeExtraKey  = "codex_fingerprint_mode"
	codexFingerprintSeedExtraKey  = "codex_fingerprint_seed"
	codexOutboundTimezoneExtraKey = "codex_outbound_timezone"
)

func canonicalCodexFingerprintSeed(value any) (string, bool) {
	raw, ok := value.(string)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	parsed, err := uuid.Parse(trimmed)
	if err != nil || parsed == uuid.Nil || trimmed != parsed.String() {
		return "", false
	}
	return trimmed, true
}

func newCodexFingerprintSeed() string {
	return uuid.NewString()
}

func stripCodexFingerprintSeed(extra map[string]any) map[string]any {
	if extra == nil {
		return nil
	}
	stripped := maps.Clone(extra)
	delete(stripped, codexFingerprintSeedExtraKey)
	return stripped
}

func codexFingerprintModeFromExtra(extra map[string]any) codexFingerprintMode {
	if extra == nil {
		return codexFingerprintOff
	}
	raw, _ := extra[codexFingerprintModeExtraKey].(string)
	switch codexFingerprintMode(strings.TrimSpace(raw)) {
	case codexFingerprintOff, codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull:
		return codexFingerprintMode(strings.TrimSpace(raw))
	default:
		return codexFingerprintOff
	}
}

func codexFingerprintModeRequiresSeed(mode codexFingerprintMode) bool {
	switch mode {
	case codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull:
		return true
	default:
		return false
	}
}

func codexFingerprintSeed(extra map[string]any) (string, bool) {
	if extra == nil {
		return "", false
	}
	return canonicalCodexFingerprintSeed(extra[codexFingerprintSeedExtraKey])
}

func prepareCodexFingerprintExtraForCreate(platform, accountType string, extra map[string]any) map[string]any {
	prepared := stripCodexFingerprintSeed(extra)
	if platform != PlatformOpenAI || accountType != AccountTypeOAuth || !codexFingerprintModeRequiresSeed(codexFingerprintModeFromExtra(prepared)) {
		return prepared
	}
	if prepared == nil {
		prepared = make(map[string]any, 1)
	}
	prepared[codexFingerprintSeedExtraKey] = newCodexFingerprintSeed()
	return prepared
}

func prepareCodexFingerprintExtraForUpdate(account *Account, extra map[string]any) map[string]any {
	prepared := stripCodexFingerprintSeed(extra)
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
		return prepared
	}
	if seed, ok := codexFingerprintSeed(account.Extra); ok {
		if prepared == nil {
			prepared = make(map[string]any, 1)
		}
		prepared[codexFingerprintSeedExtraKey] = seed
		return prepared
	}
	if codexFingerprintModeRequiresSeed(codexFingerprintModeFromExtra(prepared)) {
		if prepared == nil {
			prepared = make(map[string]any, 1)
		}
		prepared[codexFingerprintSeedExtraKey] = newCodexFingerprintSeed()
	}
	return prepared
}

func sanitizedCodexFingerprintExtraUpdates(updates map[string]any) map[string]any {
	if updates == nil {
		return nil
	}
	sanitized := maps.Clone(updates)
	delete(sanitized, codexFingerprintSeedExtraKey)
	return sanitized
}

// ShouldEnsureCodexFingerprintSeedForExtraUpdates reports whether a JSONB key-level
// extra update is enabling Codex fingerprint convergence and therefore must atomically
// preserve or create the system-managed per-account seed in the repository update.
func ShouldEnsureCodexFingerprintSeedForExtraUpdates(updates map[string]any) bool {
	if updates == nil {
		return false
	}
	return codexFingerprintModeRequiresSeed(codexFingerprintModeFromExtra(updates))
}

// GetCodexFingerprintMode 从账号 extra JSON 读取指纹收敛模式。
//
// **收敛是显式 opt-in**：未设置、空值或非法值一律按 off 处理，只有管理员
// 明确配置 device / session / full 才收敛。
//
// 历史：v0.1.175（#5553）把缺省值当作 session，导致升级后存量 OAuth 账号
// （普遍没有这个 extra 键）的每个非透传请求都被静默改写 installation /
// session / thread / turn / window 五类标识；#5555、#5556、#5582 报告的额度
// 缩水都卡在该版本边界，并有"回退 v0.1.173 即恢复"与"新账号开收敛后降额"
// 的 A/B 实测。上游的配额判定策略不可观测，因此这里取兼容安全的一侧：
// 不显式 opt-in 就保持 v0.1.175 之前的客户端身份（#5610）。
func (a *Account) GetCodexFingerprintMode() codexFingerprintMode {
	if a == nil || !a.IsOpenAIOAuth() {
		return codexFingerprintOff
	}
	if _, explicitlySet := a.Extra[codexFingerprintModeExtraKey]; explicitlySet {
		return codexFingerprintModeFromExtra(a.Extra)
	}
	return codexFingerprintSession
}

// deriveStableUUIDv4 从种子确定性派生一个 UUIDv4 格式的字符串。
// 同一种子永远返回同一值。
func deriveStableUUIDv4(seed string) string {
	h := sha256.Sum256([]byte(seed))
	b := h[:16]
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 1
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16])
}

// resolveConvergedInstallationID 返回账号级恒定的 installation_id。
// 优先使用管理员配置的真实 device_id，无则从系统管理的账号随机种子确定性派生。
func resolveConvergedInstallationID(account *Account, seed string) string {
	if account == nil {
		return ""
	}
	if deviceID := account.GetOpenAIDeviceID(); deviceID != "" {
		return deviceID
	}
	if seed == "" {
		return ""
	}
	return deriveStableUUIDv4("sub2api:codex-install-id:v2:" + seed)
}

// resolveConvergedSessionID 返回账号级恒定的 session_id。
func resolveConvergedSessionID(seed string) string {
	if seed == "" {
		return ""
	}
	return deriveStableUUIDv4("sub2api:codex-session-id:v2:" + seed)
}

// resolveConvergedThreadID 按客户端原始 session-id 确定性派生 thread_id。
// 每个真实 Codex 会话（不同客户端启动实例）获得一个独立线程，
// 模拟正常用户 spawn 子代理或开多窗口的模式。
func resolveConvergedThreadID(seed, clientSessionID string) string {
	if seed == "" || clientSessionID == "" {
		return ""
	}
	return deriveStableUUIDv4("sub2api:codex-thread-id:v2:" + seed + ":" + clientSessionID)
}

func (a *Account) resolveCodexOutboundTimezone() string {
	if a == nil || !a.IsOpenAIOAuth() {
		return "UTC"
	}
	tz := strings.TrimSpace(a.GetExtraString(codexOutboundTimezoneExtraKey))
	if tz == "" {
		return "UTC"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "UTC"
	}
	return tz
}

func codexConversationScope(promptCacheKey, clientSessionID string) string {
	if key := strings.TrimSpace(promptCacheKey); key != "" {
		return key
	}
	if sessionID := strings.TrimSpace(clientSessionID); sessionID != "" {
		return sessionID
	}
	return "default"
}

func deriveCodexConversationSeed(account *Account, apiKeyID int64, promptCacheKey, clientSessionID string) string {
	if account == nil {
		return ""
	}
	seed, ok := codexFingerprintSeed(account.Extra)
	if !ok {
		return ""
	}
	return deriveStableUUIDv4(fmt.Sprintf("sub2api:codex-conversation-seed:v2:%s:%d:%s", seed, apiKeyID, codexConversationScope(promptCacheKey, clientSessionID)))
}

func resolveConvergedScopedSessionID(account *Account, apiKeyID int64, conversationSeed string) string {
	if account == nil || conversationSeed == "" {
		return ""
	}
	return deriveStableUUIDv4(fmt.Sprintf("sub2api:codex-session-seed:v2:%d:%d:%s", account.ID, apiKeyID, conversationSeed))
}

// codexFingerprintIDs 收敛后的完整 ID 集合。
// 由 resolveCodexFingerprintIDs 一次性生成，同一个实例在头改写和体改写之间共享，
// 确保所有载体中的 turn_id 等随机字段一致。体改写时还会补记原始
// client_metadata.session_id，用于识别 root prompt_cache_key 的默认值。
type codexFingerprintIDs struct {
	accountID                     int64
	mode                          codexFingerprintMode
	installationID                string
	sessionID                     string
	threadID                      string
	turnID                        string
	windowID                      string
	turnStartedAtUnixMs           int64
	originalBodySessionID         string
	originalBodySessionIDCaptured bool
	conversationID                string
	promptCacheKey                string
	timezone                      string
	scoped                        bool
}

// resolveCodexFingerprintIDs 按收敛模式计算出站 ID 集合。
// clientSessionID 是客户端原始的 session-id 头值（连字符形式），用于 session 模式下
// 的 thread_id 派生——每个真实 Codex 会话得到一个独立线程。
// 返回 nil 表示 off 模式，不需要改写。
// 注意：包含随机生成的 turn_id，调用方必须只调用一次并共享结果给头改写和体改写。
func resolveCodexFingerprintIDs(account *Account, clientSessionID string, mode codexFingerprintMode) *codexFingerprintIDs {
	if account == nil || mode == codexFingerprintOff {
		return nil
	}
	seed, ok := codexFingerprintSeed(account.Extra)
	if !ok {
		return nil
	}

	ids := &codexFingerprintIDs{
		accountID:           account.ID,
		mode:                mode,
		turnStartedAtUnixMs: time.Now().UnixMilli(),
		timezone:            account.resolveCodexOutboundTimezone(),
	}

	ids.installationID = resolveConvergedInstallationID(account, seed)
	if ids.installationID == "" {
		return nil
	}

	switch mode {
	case codexFingerprintDevice:
		return ids

	case codexFingerprintSession:
		ids.sessionID = resolveConvergedSessionID(seed)
		ids.threadID = resolveConvergedThreadID(seed, clientSessionID)
		if ids.threadID == "" {
			ids.threadID = ids.sessionID
		}
		ids.turnID = uuid.Must(uuid.NewV7()).String()
		ids.windowID = ids.threadID + ":0"
		return ids

	case codexFingerprintFull:
		ids.sessionID = resolveConvergedSessionID(seed)
		ids.threadID = ids.sessionID
		ids.turnID = uuid.Must(uuid.NewV7()).String()
		ids.windowID = ids.threadID + ":0"
		return ids
	}

	return nil
}

func resolveCodexFingerprintIDsWithScope(account *Account, apiKeyID int64, promptCacheKey, clientSessionID string, mode codexFingerprintMode) *codexFingerprintIDs {
	if account == nil || mode == codexFingerprintOff {
		return nil
	}
	seed, ok := codexFingerprintSeed(account.Extra)
	if !ok {
		return nil
	}
	ids := &codexFingerprintIDs{accountID: account.ID, mode: mode, turnStartedAtUnixMs: time.Now().UnixMilli(), timezone: account.resolveCodexOutboundTimezone(), scoped: true}
	ids.installationID = resolveConvergedInstallationID(account, seed)
	if ids.installationID == "" {
		return nil
	}
	conversationSeed := deriveCodexConversationSeed(account, apiKeyID, promptCacheKey, clientSessionID)
	if strings.TrimSpace(promptCacheKey) != "" {
		ids.promptCacheKey = conversationSeed
	}
	switch mode {
	case codexFingerprintDevice:
		return ids
	case codexFingerprintSession:
		ids.sessionID = resolveConvergedScopedSessionID(account, apiKeyID, conversationSeed)
		ids.conversationID = ids.sessionID
		ids.threadID = deriveStableUUIDv4(fmt.Sprintf("sub2api:codex-thread-id:v2:%d:%d:%s", account.ID, apiKeyID, conversationSeed))
		if ids.threadID == "" {
			ids.threadID = ids.sessionID
		}
	case codexFingerprintFull:
		ids.sessionID = resolveConvergedScopedSessionID(account, apiKeyID, conversationSeed)
		ids.conversationID = ids.sessionID
		ids.threadID = ids.sessionID
	default:
		return nil
	}
	ids.turnID = uuid.Must(uuid.NewV7()).String()
	ids.windowID = ids.threadID + ":0"
	return ids
}

// extractClientSessionID 从请求头中提取客户端原始的会话标识。
// 优先取 session-id（连字符形式，Codex CLI 标准），回退到 session_id（下划线形式）。
// 返回的值尚未被 isolateOpenAISessionID 改写，是客户端的真实标识。
func extractClientSessionID(h http.Header) string {
	if v := strings.TrimSpace(h.Get("session-id")); v != "" {
		return v
	}
	return strings.TrimSpace(h.Get("session_id"))
}

// resolveCodexFingerprintIDsFromRequest 从客户端原始请求头中提取 session-id，
// 结合账号配置一次性解析收敛 ID 集合。调用方应将返回的 ids 同时传给
// applyCodexFingerprintHeaders 和 applyCodexFingerprintClientMetadata。
func resolveCodexFingerprintIDsFromRequest(account *Account, clientHeaders http.Header) *codexFingerprintIDs {
	if account == nil {
		return nil
	}
	mode := account.GetCodexFingerprintMode()
	if mode == codexFingerprintOff {
		return nil
	}
	clientSessionID := ""
	if clientHeaders != nil {
		clientSessionID = extractClientSessionID(clientHeaders)
	}
	return resolveCodexFingerprintIDs(account, clientSessionID, mode)
}

func resolveCodexFingerprintIDsFromRequestWithScope(account *Account, clientHeaders http.Header, apiKeyID int64, promptCacheKey string) *codexFingerprintIDs {
	if account == nil {
		return nil
	}
	return resolveCodexFingerprintIDsWithScope(account, apiKeyID, promptCacheKey, extractClientSessionID(clientHeaders), account.GetCodexFingerprintMode())
}

// applyCodexFingerprintHeaders 按预计算的收敛 ID 改写出站 HTTP 头中的设备指纹。
// 在 buildUpstreamRequest 的白名单透传之后、enforceCodexIdentityHeaders 之前调用。
func applyCodexFingerprintHeaders(h http.Header, ids *codexFingerprintIDs) {
	if h == nil || ids == nil {
		return
	}

	// 所有非 off 模式都收敛 installation_id
	h.Set("x-codex-installation-id", ids.installationID)
	if ids.timezone != "" {
		h.Set("x-codex-timezone", ids.timezone)
	}

	if ids.mode == codexFingerprintDevice {
		rewriteCodexTurnMetadataFields(h, map[string]any{
			"installation_id": ids.installationID,
			"timezone":        ids.timezone,
		})
		return
	}

	// session / full 模式：改写所有相关头
	h.Set("x-codex-window-id", ids.windowID)
	h.Set("x-client-request-id", ids.threadID)
	// 连字符形式和下划线形式都改写，保证一致
	h.Set("session-id", ids.sessionID)
	h.Set("session_id", ids.sessionID)
	h.Set("thread-id", ids.threadID)
	h.Set("conversation_id", ids.conversationID)

	rewriteCodexTurnMetadataFields(h, map[string]any{
		"installation_id":         ids.installationID,
		"session_id":              ids.sessionID,
		"thread_id":               ids.threadID,
		"turn_id":                 ids.turnID,
		"window_id":               ids.windowID,
		"turn_started_at_unix_ms": ids.turnStartedAtUnixMs,
		"timezone":                ids.timezone,
	})
}

// rewriteCodexTurnMetadataFields 解析 x-codex-turn-metadata 头中的 JSON，
// 替换指定字段后回写。合法对象保留未指定字段（如 sandbox、thread_source）；
// 非法/非对象值重建为最小合法 metadata，避免 flat 与 embedded identity 分裂。
func rewriteCodexTurnMetadataFields(h http.Header, fields map[string]any) {
	raw := strings.TrimSpace(h.Get("x-codex-turn-metadata"))
	if raw == "" {
		return
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil || metadata == nil {
		metadata = make(map[string]any, len(fields))
	}
	for k, v := range fields {
		metadata[k] = v
	}
	rebuilt, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	h.Set("x-codex-turn-metadata", string(rebuilt))
}

// applyCodexFingerprintClientMetadata 按预计算的收敛 ID 改写请求体中的 client_metadata。
// 使用与头改写相同的 ids 实例，确保 turn_id 等随机字段一致。
func applyCodexFingerprintClientMetadata(reqBody map[string]any, ids *codexFingerprintIDs) bool {
	if reqBody == nil || ids == nil {
		return false
	}

	captureCodexFingerprintOriginalBodySessionID(ids, reqBody["client_metadata"])
	existing, _ := reqBody["client_metadata"].(map[string]any)
	if existing == nil {
		existing = make(map[string]any)
	}

	modified := false
	if applyCodexFingerprintToClientMetadataMap(existing, ids) {
		reqBody["client_metadata"] = existing
		modified = true
	}
	if applyCodexFingerprintPromptCacheKey(reqBody, ids) {
		modified = true
	}
	return modified
}

// applyCodexFingerprintToClientMetadataMap 是 client_metadata 改写的共享核心，
// map 版（非透传，body 已解码）与 raw 字节版（透传热路径）都经由它，保证两条
// 路径的收敛语义永不漂移。
func applyCodexFingerprintToClientMetadataMap(existing map[string]any, ids *codexFingerprintIDs) bool {
	if existing == nil || ids == nil {
		return false
	}

	modified := false

	if ids.installationID != "" {
		existing["x-codex-installation-id"] = ids.installationID
		modified = true
	}
	if ids.timezone != "" {
		existing["timezone"] = ids.timezone
		modified = true
	}

	if ids.mode == codexFingerprintDevice {
		rewriteClientMetadataEmbeddedTurnMetadata(existing, map[string]any{
			"installation_id": ids.installationID,
			"timezone":        ids.timezone,
		})
		return modified
	}

	// session / full 模式
	existing["session_id"] = ids.sessionID
	existing["conversation_id"] = ids.conversationID
	existing["thread_id"] = ids.threadID
	existing["turn_id"] = ids.turnID
	existing["x-codex-window-id"] = ids.windowID

	rewriteClientMetadataEmbeddedTurnMetadata(existing, map[string]any{
		"installation_id":         ids.installationID,
		"session_id":              ids.sessionID,
		"thread_id":               ids.threadID,
		"turn_id":                 ids.turnID,
		"window_id":               ids.windowID,
		"turn_started_at_unix_ms": ids.turnStartedAtUnixMs,
		"timezone":                ids.timezone,
	})
	return true
}

func captureCodexFingerprintOriginalBodySessionID(ids *codexFingerprintIDs, clientMetadata any) {
	if ids == nil || ids.originalBodySessionIDCaptured {
		return
	}
	ids.originalBodySessionIDCaptured = true
	if clientMetadata == nil {
		return
	}
	switch metadata := clientMetadata.(type) {
	case map[string]any:
		if sessionID, ok := metadata["session_id"].(string); ok {
			ids.originalBodySessionID = strings.TrimSpace(sessionID)
		}
	case map[string]string:
		ids.originalBodySessionID = strings.TrimSpace(metadata["session_id"])
	}
}

func captureCodexFingerprintOriginalBodySessionIDRaw(ids *codexFingerprintIDs, value gjson.Result) {
	if ids == nil || ids.originalBodySessionIDCaptured {
		return
	}
	ids.originalBodySessionIDCaptured = true
	if value.Exists() && value.Type == gjson.String {
		ids.originalBodySessionID = strings.TrimSpace(value.String())
	}
}

func shouldRewriteCodexFingerprintPromptCacheKey(ids *codexFingerprintIDs, promptCacheKey string) bool {
	if ids == nil || !ids.originalBodySessionIDCaptured || ids.originalBodySessionID == "" || ids.sessionID == "" {
		return false
	}
	if ids.mode != codexFingerprintSession && ids.mode != codexFingerprintFull {
		return false
	}
	return promptCacheKey == ids.originalBodySessionID
}

func applyCodexFingerprintPromptCacheKey(reqBody map[string]any, ids *codexFingerprintIDs) bool {
	if reqBody == nil {
		return false
	}
	promptCacheKey, ok := reqBody["prompt_cache_key"].(string)
	if !ok || strings.TrimSpace(promptCacheKey) == "" {
		return false
	}
	if ids.promptCacheKey != "" {
		if !ids.scoped && !shouldRewriteCodexFingerprintPromptCacheKey(ids, promptCacheKey) {
			return false
		}
		reqBody["prompt_cache_key"] = ids.promptCacheKey
		return promptCacheKey != ids.promptCacheKey
	}
	if !shouldRewriteCodexFingerprintPromptCacheKey(ids, promptCacheKey) {
		return false
	}
	if promptCacheKey == ids.sessionID {
		return false
	}
	reqBody["prompt_cache_key"] = ids.sessionID
	return true
}

// applyCodexFingerprintClientMetadataRaw 在原始 JSON 字节上改写 client_metadata，
// 供透传路径使用——透传是热路径，禁止对可能高达数十 MB 的 body 做全量
// Unmarshal（见 forwardOpenAIPassthrough 的轻量提取注释）。实现为：gjson 提取
// client_metadata 小对象单独解码，经共享核心改写后 sjson 一次性拼回，body
// 其余字节原样保留；root prompt_cache_key 仅在可证明是 body session 默认值时
// 做标量改写。语义与 applyCodexFingerprintClientMetadata 逐点一致（含
// "非对象值整体替换为收敛集合"的行为）。
func applyCodexFingerprintClientMetadataRaw(body []byte, ids *codexFingerprintIDs) ([]byte, bool, error) {
	if len(body) == 0 || ids == nil {
		return body, false, nil
	}
	// 非 JSON 对象的 body（数组/标量/畸形）没有 client_metadata 语义，
	// sjson 在这类根上写字段会改写整体结构，直接放行保持原样。
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		captureCodexFingerprintOriginalBodySessionIDRaw(ids, gjson.Result{})
		return body, false, nil
	}

	existing := map[string]any{}
	if cm := gjson.GetBytes(body, "client_metadata"); cm.IsObject() {
		captureCodexFingerprintOriginalBodySessionIDRaw(ids, gjson.GetBytes(body, "client_metadata.session_id"))
		if err := json.Unmarshal([]byte(cm.Raw), &existing); err != nil {
			return body, false, fmt.Errorf("decode client_metadata for fingerprint: %w", err)
		}
	} else {
		captureCodexFingerprintOriginalBodySessionIDRaw(ids, gjson.Result{})
	}

	next := body
	modified := false
	if applyCodexFingerprintToClientMetadataMap(existing, ids) {
		raw, err := json.Marshal(existing)
		if err != nil {
			return body, false, fmt.Errorf("encode converged client_metadata: %w", err)
		}
		var setErr error
		next, setErr = sjson.SetRawBytes(body, "client_metadata", raw)
		if setErr != nil {
			return body, false, fmt.Errorf("splice converged client_metadata: %w", setErr)
		}
		modified = true
	}
	promptCacheKey := gjson.GetBytes(body, "prompt_cache_key")
	if promptCacheKey.Exists() && promptCacheKey.Type == gjson.String && strings.TrimSpace(promptCacheKey.String()) != "" {
		replacement := ""
		if ids.promptCacheKey != "" && (ids.scoped || shouldRewriteCodexFingerprintPromptCacheKey(ids, promptCacheKey.String())) {
			replacement = ids.promptCacheKey
		} else if shouldRewriteCodexFingerprintPromptCacheKey(ids, promptCacheKey.String()) {
			replacement = ids.sessionID
		}
		if replacement == "" || replacement == promptCacheKey.String() {
			return next, modified, nil
		}
		rewritten, err := sjson.SetBytes(next, "prompt_cache_key", replacement)
		if err != nil {
			return body, false, fmt.Errorf("splice converged prompt_cache_key: %w", err)
		}
		next = rewritten
		modified = true
	}
	return next, modified, nil
}

// rewriteClientMetadataEmbeddedTurnMetadata 改写 client_metadata 中内嵌的
// x-codex-turn-metadata JSON 字符串里的指定字段。非法/非对象值会重建，
// 避免 flat client_metadata 与 embedded metadata 暴露两套身份。
func rewriteClientMetadataEmbeddedTurnMetadata(clientMetadata map[string]any, fields map[string]any) {
	raw, ok := clientMetadata["x-codex-turn-metadata"].(string)
	if !ok || raw == "" {
		return
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil || metadata == nil {
		metadata = make(map[string]any, len(fields))
	}
	for k, v := range fields {
		metadata[k] = v
	}
	if rebuilt, err := json.Marshal(metadata); err == nil {
		clientMetadata["x-codex-turn-metadata"] = string(rebuilt)
	}
}

func sanitizeCodexClientMetadata(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	blocked := map[string]struct{}{"api_key": {}, "api-key": {}, "apikey": {}, "authorization": {}, "authorization_header": {}, "proxy_authorization": {}, "proxy-authorization": {}, "proxy_url": {}, "proxyurl": {}, "base_url": {}, "baseurl": {}, "endpoint": {}, "endpoint_url": {}, "endpointurl": {}, "hostname": {}, "host": {}}
	changed := false
	for key, value := range metadata {
		if _, forbidden := blocked[strings.ToLower(strings.TrimSpace(key))]; forbidden {
			delete(metadata, key)
			changed = true
			continue
		}
		switch nested := value.(type) {
		case map[string]any:
			changed = sanitizeCodexClientMetadata(nested) || changed
		case []any:
			for _, item := range nested {
				if child, ok := item.(map[string]any); ok {
					changed = sanitizeCodexClientMetadata(child) || changed
				}
			}
		case string:
			if !strings.EqualFold(key, "x-codex-turn-metadata") || strings.TrimSpace(nested) == "" {
				continue
			}
			var embedded map[string]any
			if json.Unmarshal([]byte(nested), &embedded) == nil && sanitizeCodexClientMetadata(embedded) {
				if rebuilt, err := json.Marshal(embedded); err == nil {
					metadata[key] = string(rebuilt)
					changed = true
				}
			}
		}
	}
	return changed
}

func sanitizeCodexRequestClientMetadata(reqBody map[string]any) bool {
	if reqBody == nil {
		return false
	}
	metadata, _ := reqBody["client_metadata"].(map[string]any)
	return sanitizeCodexClientMetadata(metadata)
}

func sanitizeCodexRequestClientMetadataRaw(body []byte) ([]byte, bool, error) {
	if len(body) == 0 || !gjson.ParseBytes(body).IsObject() {
		return body, false, nil
	}
	raw := gjson.GetBytes(body, "client_metadata")
	if !raw.IsObject() {
		return body, false, nil
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(raw.Raw), &metadata); err != nil {
		return body, false, fmt.Errorf("decode client_metadata for sanitization: %w", err)
	}
	if !sanitizeCodexClientMetadata(metadata) {
		return body, false, nil
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return body, false, err
	}
	next, err := sjson.SetRawBytes(body, "client_metadata", encoded)
	if err != nil {
		return body, false, err
	}
	return next, true, nil
}

type codexClientIdentifiers struct{ installationID, sessionID, conversationID, threadID, windowID, promptCacheKey string }

func captureCodexClientIdentifiers(c *gin.Context, reqBody map[string]any) {
	if c == nil || c.Request == nil {
		return
	}
	ids := &codexClientIdentifiers{installationID: strings.TrimSpace(c.Request.Header.Get("x-codex-installation-id")), sessionID: extractClientSessionID(c.Request.Header), conversationID: strings.TrimSpace(c.Request.Header.Get("conversation_id")), threadID: strings.TrimSpace(c.Request.Header.Get("thread-id")), windowID: strings.TrimSpace(c.Request.Header.Get("x-codex-window-id"))}
	if reqBody != nil {
		ids.promptCacheKey, _ = reqBody["prompt_cache_key"].(string)
		if metadata, _ := reqBody["client_metadata"].(map[string]any); metadata != nil {
			if ids.installationID == "" {
				ids.installationID, _ = metadata["x-codex-installation-id"].(string)
			}
			if ids.sessionID == "" {
				ids.sessionID, _ = metadata["session_id"].(string)
			}
			if ids.conversationID == "" {
				ids.conversationID, _ = metadata["conversation_id"].(string)
			}
			if ids.threadID == "" {
				ids.threadID, _ = metadata["thread_id"].(string)
			}
			if ids.windowID == "" {
				ids.windowID, _ = metadata["x-codex-window-id"].(string)
			}
		}
	}
	c.Set(codexClientIdentifiersContextKey, ids)
}

func capturedCodexClientPromptCacheKey(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, ok := c.Get(codexClientIdentifiersContextKey)
	ids, typed := value.(*codexClientIdentifiers)
	if !ok || !typed || ids == nil {
		return ""
	}
	return strings.TrimSpace(ids.promptCacheKey)
}

func captureCodexClientIdentifiersRaw(c *gin.Context, body []byte) {
	if c == nil || c.Request == nil {
		return
	}
	bodyMap := map[string]any{}
	if gjson.ParseBytes(body).IsObject() {
		if key := gjson.GetBytes(body, "prompt_cache_key"); key.Type == gjson.String {
			bodyMap["prompt_cache_key"] = key.String()
		}
		if raw := gjson.GetBytes(body, "client_metadata"); raw.IsObject() {
			metadata := map[string]any{}
			if json.Unmarshal([]byte(raw.Raw), &metadata) == nil {
				bodyMap["client_metadata"] = metadata
			}
		}
	}
	captureCodexClientIdentifiers(c, bodyMap)
}

func restoreCodexClientResponseIdentifiers(c *gin.Context, data []byte) []byte {
	if c == nil || len(data) == 0 {
		return data
	}
	value, ok := c.Get(codexClientIdentifiersContextKey)
	original, typed := value.(*codexClientIdentifiers)
	if !ok || !typed || original == nil {
		return data
	}
	fingerprint, ok := c.Get(codexFingerprintIDsContextKey)
	ids, typed := fingerprint.(*codexFingerprintIDs)
	if !ok || !typed || ids == nil {
		return data
	}
	var payload any
	if json.Unmarshal(data, &payload) != nil {
		return data
	}
	if !restoreCodexClientIdentifierValues(payload, original, ids) {
		return data
	}
	rebuilt, err := json.Marshal(payload)
	if err != nil {
		return data
	}
	return rebuilt
}

func restoreCodexClientIdentifierValues(value any, original *codexClientIdentifiers, ids *codexFingerprintIDs) bool {
	switch typed := value.(type) {
	case []any:
		changed := false
		for _, item := range typed {
			changed = restoreCodexClientIdentifierValues(item, original, ids) || changed
		}
		return changed
	case map[string]any:
		changed := false
		for key, raw := range typed {
			if nested, ok := raw.(map[string]any); ok {
				changed = restoreCodexClientIdentifierValues(nested, original, ids) || changed
				continue
			}
			if nested, ok := raw.([]any); ok {
				changed = restoreCodexClientIdentifierValues(nested, original, ids) || changed
				continue
			}
			stringValue, ok := raw.(string)
			if !ok {
				continue
			}
			var replacement string
			matched := false
			switch strings.ToLower(key) {
			case "x-codex-installation-id", "installation_id":
				matched, replacement = stringValue == ids.installationID, original.installationID
			case "session_id", "session-id":
				matched, replacement = stringValue == ids.sessionID, original.sessionID
			case "conversation_id", "conversation-id":
				matched, replacement = stringValue == ids.conversationID, original.conversationID
			case "thread_id", "thread-id":
				matched, replacement = stringValue == ids.threadID, original.threadID
			case "window_id", "x-codex-window-id":
				matched, replacement = stringValue == ids.windowID, original.windowID
			case "prompt_cache_key":
				matched, replacement = stringValue == ids.promptCacheKey, original.promptCacheKey
			}
			if matched {
				if replacement == "" {
					delete(typed, key)
				} else {
					typed[key] = replacement
				}
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}
