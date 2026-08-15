package service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
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

// stageCodexFingerprintIDs 将本 attempt 解析出的收敛 ID 暂存到 gin context。
// 必须无条件覆写（含 nil）：failover 从收敛账号切到 off 账号时，上一账号的
// IDs 不得残留并被误应用到新账号的出站头（typed-nil 由应用侧 nil 守卫吸收）。
func stageCodexFingerprintIDs(c *gin.Context, ids *codexFingerprintIDs) {
	if c != nil {
		c.Set(codexFingerprintIDsContextKey, ids)
	}
}

// applyStagedCodexFingerprintHeaders 读取 context 暂存的收敛 ID 并改写出站头。
// 非透传与透传两个请求构造器共用本函数，防止应用语义漂移。仅 OAuth 账号
// 生效（stale 键在账号类型混合 failover 下由该门挡住）。
func applyStagedCodexFingerprintHeaders(c *gin.Context, account *Account, h http.Header) {
	if c == nil || account == nil || account.Type != AccountTypeOAuth {
		return
	}
	value, ok := c.Get(codexFingerprintIDsContextKey)
	if !ok {
		return
	}
	if ids, ok := value.(*codexFingerprintIDs); ok {
		applyCodexFingerprintHeaders(h, ids)
	}
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
	codexFingerprintModeExtraKey     = "codex_fingerprint_mode"
	codexOutboundTimezoneExtraKey    = "codex_outbound_timezone"
	codexClientIdentifiersContextKey = "codex_client_identifiers"
)

// GetCodexFingerprintMode 从账号 extra JSON 读取指纹收敛模式。
//
// 未设置、空值或非法值时使用 session，确保 Codex OAuth 的各类出站载体使用
// 同一组稳定设备与会话标识；显式 off 可关闭收敛。
func (a *Account) GetCodexFingerprintMode() codexFingerprintMode {
	if a == nil || !a.IsOpenAIOAuth() {
		return codexFingerprintOff
	}
	raw := strings.TrimSpace(a.GetExtraString(codexFingerprintModeExtraKey))
	switch codexFingerprintMode(raw) {
	case codexFingerprintOff, codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull:
		return codexFingerprintMode(raw)
	default:
		return codexFingerprintSession
	}
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
// 优先使用管理员配置的真实 device_id，无则从 accountID 确定性派生。
func resolveConvergedInstallationID(account *Account) string {
	if account == nil {
		return ""
	}
	if deviceID := account.GetOpenAIDeviceID(); deviceID != "" {
		return deviceID
	}
	return deriveStableUUIDv4(fmt.Sprintf("sub2api:codex-install-id:v1:%d", account.ID))
}

// resolveConvergedSessionID 返回账号级恒定的 session_id。
func resolveConvergedSessionID(account *Account) string {
	if account == nil {
		return ""
	}
	return deriveStableUUIDv4(fmt.Sprintf("sub2api:codex-session-id:v1:%d", account.ID))
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
	return deriveStableUUIDv4(fmt.Sprintf(
		"sub2api:codex-conversation-seed:v1:%d:%d:%s",
		account.ID, apiKeyID, codexConversationScope(promptCacheKey, clientSessionID),
	))
}

func resolveConvergedScopedSessionID(account *Account, apiKeyID int64, conversationSeed string) string {
	if account == nil {
		return ""
	}
	return deriveStableUUIDv4(fmt.Sprintf(
		"sub2api:codex-session-seed:v1:%d:%d:%s", account.ID, apiKeyID, conversationSeed,
	))
}

// resolveConvergedThreadID 按客户端原始 session-id 确定性派生 thread_id。
// 每个真实 Codex 会话（不同客户端启动实例）获得一个独立线程，
// 模拟正常用户 spawn 子代理或开多窗口的模式。
func resolveConvergedThreadID(account *Account, clientSessionID string) string {
	if account == nil || clientSessionID == "" {
		return ""
	}
	return deriveStableUUIDv4(fmt.Sprintf("sub2api:codex-thread-id:v1:%d:%s", account.ID, clientSessionID))
}

// codexFingerprintIDs 收敛后的完整 ID 集合。
// 由 resolveCodexFingerprintIDs 一次性生成，同一个实例在头改写和体改写之间共享，
// 确保所有载体中的 turn_id 等随机字段一致。
type codexFingerprintIDs struct {
	mode           codexFingerprintMode
	installationID string
	sessionID      string
	conversationID string
	threadID       string
	turnID         string
	windowID       string
	promptCacheKey string
	timezone       string
}

// resolveCodexFingerprintIDs 按收敛模式计算出站 ID 集合。
// clientSessionID 是客户端原始的 session-id 头值（连字符形式），用于 session 模式下
// 的 thread_id 派生——每个真实 Codex 会话得到一个独立线程。
// 返回 nil 表示 off 模式，不需要改写。
// 注意：包含随机生成的 turn_id，调用方必须只调用一次并共享结果给头改写和体改写。
func resolveCodexFingerprintIDs(account *Account, clientSessionID string, mode codexFingerprintMode) *codexFingerprintIDs {
	if mode == codexFingerprintOff {
		return nil
	}
	ids := &codexFingerprintIDs{
		mode:           mode,
		installationID: resolveConvergedInstallationID(account),
		timezone:       account.resolveCodexOutboundTimezone(),
	}
	if ids.installationID == "" {
		return nil
	}
	switch mode {
	case codexFingerprintDevice:
		return ids
	case codexFingerprintSession:
		ids.sessionID = resolveConvergedSessionID(account)
		ids.conversationID = ids.sessionID
		ids.threadID = resolveConvergedThreadID(account, clientSessionID)
		if ids.threadID == "" {
			ids.threadID = ids.sessionID
		}
	case codexFingerprintFull:
		ids.sessionID = resolveConvergedSessionID(account)
		ids.conversationID = ids.sessionID
		ids.threadID = ids.sessionID
	default:
		return nil
	}
	ids.turnID = uuid.Must(uuid.NewV7()).String()
	ids.windowID = ids.threadID + ":0"
	return ids
}

func resolveCodexFingerprintIDsWithScope(account *Account, apiKeyID int64, promptCacheKey, clientSessionID string, mode codexFingerprintMode) *codexFingerprintIDs {
	if mode == codexFingerprintOff {
		return nil
	}

	ids := &codexFingerprintIDs{mode: mode, timezone: account.resolveCodexOutboundTimezone()}

	ids.installationID = resolveConvergedInstallationID(account)
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
		ids.turnID = uuid.Must(uuid.NewV7()).String()
		ids.windowID = ids.threadID + ":0"
		return ids

	case codexFingerprintFull:
		ids.sessionID = resolveConvergedScopedSessionID(account, apiKeyID, conversationSeed)
		ids.conversationID = ids.sessionID
		ids.threadID = ids.sessionID
		ids.turnID = uuid.Must(uuid.NewV7()).String()
		ids.windowID = ids.threadID + ":0"
		return ids
	}

	return nil
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
	return resolveCodexFingerprintIDs(account, extractClientSessionID(clientHeaders), account.GetCodexFingerprintMode())
}

func resolveCodexFingerprintIDsFromRequestWithScope(account *Account, clientHeaders http.Header, apiKeyID int64, promptCacheKey string) *codexFingerprintIDs {
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
	return resolveCodexFingerprintIDsWithScope(account, apiKeyID, promptCacheKey, clientSessionID, mode)
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
	h.Set("conversation_id", ids.conversationID)
	h.Set("thread-id", ids.threadID)

	rewriteCodexTurnMetadataFields(h, map[string]any{
		"installation_id":         ids.installationID,
		"session_id":              ids.sessionID,
		"thread_id":               ids.threadID,
		"turn_id":                 ids.turnID,
		"window_id":               ids.windowID,
		"turn_started_at_unix_ms": time.Now().UnixMilli(),
		"timezone":                ids.timezone,
	})
}

// rewriteCodexTurnMetadataFields 解析 x-codex-turn-metadata 头中的 JSON，
// 替换指定字段后回写。保留未指定字段原样（如 sandbox、thread_source 等）。
func rewriteCodexTurnMetadataFields(h http.Header, fields map[string]any) {
	raw := strings.TrimSpace(h.Get("x-codex-turn-metadata"))
	if raw == "" {
		return
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return
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

	existing, _ := reqBody["client_metadata"].(map[string]any)
	if existing == nil {
		existing = make(map[string]any)
	}

	if !applyCodexFingerprintToClientMetadataMap(existing, ids) {
		return false
	}
	if ids.promptCacheKey != "" {
		reqBody["prompt_cache_key"] = ids.promptCacheKey
	}
	reqBody["client_metadata"] = existing
	return true
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
		sanitizeCodexClientMetadata(existing)
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
		"turn_started_at_unix_ms": time.Now().UnixMilli(),
		"timezone":                ids.timezone,
	})
	sanitizeCodexClientMetadata(existing)
	return true
}

// applyCodexFingerprintClientMetadataRaw 在原始 JSON 字节上改写 client_metadata，
// 供透传路径使用——透传是热路径，禁止对可能高达数十 MB 的 body 做全量
// Unmarshal（见 forwardOpenAIPassthrough 的轻量提取注释）。实现为：gjson 提取
// client_metadata 小对象单独解码，经共享核心改写后 sjson 一次性拼回，body
// 其余字节原样保留。语义与 applyCodexFingerprintClientMetadata 逐点一致
// （含"非对象值整体替换为收敛集合"的行为）。
func applyCodexFingerprintClientMetadataRaw(body []byte, ids *codexFingerprintIDs) ([]byte, bool, error) {
	if len(body) == 0 || ids == nil {
		return body, false, nil
	}
	// 非 JSON 对象的 body（数组/标量/畸形）没有 client_metadata 语义，
	// sjson 在这类根上写字段会改写整体结构，直接放行保持原样。
	if !gjson.ParseBytes(body).IsObject() {
		return body, false, nil
	}

	existing := map[string]any{}
	if cm := gjson.GetBytes(body, "client_metadata"); cm.IsObject() {
		if err := json.Unmarshal([]byte(cm.Raw), &existing); err != nil {
			return body, false, fmt.Errorf("decode client_metadata for fingerprint: %w", err)
		}
	}

	if !applyCodexFingerprintToClientMetadataMap(existing, ids) {
		return body, false, nil
	}
	if ids.promptCacheKey != "" {
		var err error
		body, err = sjson.SetBytes(body, "prompt_cache_key", ids.promptCacheKey)
		if err != nil {
			return body, false, fmt.Errorf("set converged prompt cache key: %w", err)
		}
	}

	raw, err := json.Marshal(existing)
	if err != nil {
		return body, false, fmt.Errorf("encode converged client_metadata: %w", err)
	}
	next, err := sjson.SetRawBytes(body, "client_metadata", raw)
	if err != nil {
		return body, false, fmt.Errorf("splice converged client_metadata: %w", err)
	}
	return next, true, nil
}

// rewriteClientMetadataEmbeddedTurnMetadata 改写 client_metadata 中内嵌的
// x-codex-turn-metadata JSON 字符串里的指定字段。
func rewriteClientMetadataEmbeddedTurnMetadata(clientMetadata map[string]any, fields map[string]any) {
	raw, ok := clientMetadata["x-codex-turn-metadata"].(string)
	if !ok || raw == "" {
		return
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return
	}
	for k, v := range fields {
		metadata[k] = v
	}
	if rebuilt, err := json.Marshal(metadata); err == nil {
		clientMetadata["x-codex-turn-metadata"] = string(rebuilt)
	}
}

// sanitizeCodexClientMetadata removes relay addressing and credential material
// from client metadata before it reaches the Codex OAuth upstream.
func sanitizeCodexClientMetadata(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	blocked := map[string]struct{}{
		"api_key": {}, "api-key": {}, "apikey": {}, "authorization": {}, "authorization_header": {}, "authorizationheader": {},
		"proxy_authorization": {}, "proxy-authorization": {}, "proxyauthorization": {},
		"base_url": {}, "baseurl": {}, "endpoint": {}, "endpoint_url": {}, "endpointurl": {}, "hostname": {}, "host": {}, "proxy_url": {}, "proxyurl": {},
	}
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
		return body, false, fmt.Errorf("encode sanitized client_metadata: %w", err)
	}
	next, err := sjson.SetRawBytes(body, "client_metadata", encoded)
	if err != nil {
		return body, false, fmt.Errorf("splice sanitized client_metadata: %w", err)
	}
	return next, true, nil
}

type codexClientIdentifiers struct {
	installationID string
	sessionID      string
	conversationID string
	threadID       string
	windowID       string
	promptCacheKey string
}

func captureCodexClientIdentifiers(c *gin.Context, reqBody map[string]any) {
	if c == nil || c.Request == nil {
		return
	}
	identifiers := &codexClientIdentifiers{
		installationID: strings.TrimSpace(c.Request.Header.Get("x-codex-installation-id")),
		sessionID:      extractClientSessionID(c.Request.Header),
		conversationID: strings.TrimSpace(c.Request.Header.Get("conversation_id")),
		threadID:       strings.TrimSpace(c.Request.Header.Get("thread-id")),
		windowID:       strings.TrimSpace(c.Request.Header.Get("x-codex-window-id")),
	}
	if reqBody != nil {
		identifiers.promptCacheKey, _ = reqBody["prompt_cache_key"].(string)
		if metadata, _ := reqBody["client_metadata"].(map[string]any); metadata != nil {
			if identifiers.installationID == "" {
				identifiers.installationID, _ = metadata["x-codex-installation-id"].(string)
			}
			if identifiers.sessionID == "" {
				identifiers.sessionID, _ = metadata["session_id"].(string)
			}
			if identifiers.conversationID == "" {
				identifiers.conversationID, _ = metadata["conversation_id"].(string)
			}
			if identifiers.threadID == "" {
				identifiers.threadID, _ = metadata["thread_id"].(string)
			}
			if identifiers.windowID == "" {
				identifiers.windowID, _ = metadata["x-codex-window-id"].(string)
			}
		}
	}
	c.Set(codexClientIdentifiersContextKey, identifiers)
}

func capturedCodexClientPromptCacheKey(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, ok := c.Get(codexClientIdentifiersContextKey)
	if !ok {
		return ""
	}
	identifiers, ok := value.(*codexClientIdentifiers)
	if !ok || identifiers == nil {
		return ""
	}
	return strings.TrimSpace(identifiers.promptCacheKey)
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
	originalValue, ok := c.Get(codexClientIdentifiersContextKey)
	if !ok {
		return data
	}
	original, ok := originalValue.(*codexClientIdentifiers)
	if !ok || original == nil {
		return data
	}
	fingerprintValue, ok := c.Get(codexFingerprintIDsContextKey)
	if !ok {
		return data
	}
	ids, ok := fingerprintValue.(*codexFingerprintIDs)
	if !ok || ids == nil {
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
			switch nested := raw.(type) {
			case map[string]any, []any:
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
