package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// CyberSessionBlockStore 是 cyber 会话屏蔽表的存取接口。
// repository 层 gatewayCache 附带实现（类型断言探测接入，不改 GatewayCache
// 共享接口）；测试 stub 不实现时屏蔽能力自动降级关闭。
type CyberSessionBlockStore interface {
	SetCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string, ttl time.Duration) error
	IsCyberSessionScopeActive(ctx context.Context, scopeKey string) (bool, error)
	FindCyberSessionBlocked(ctx context.Context, keys []string) (string, error)
}

// CyberSessionIdentityResolution is the non-sensitive result used by gateway
// admission and WebSocket connection binding. BlockKey and LookupKeys are
// one-way hashes; the client-provided identity is never exposed.
type CyberSessionIdentityResolution struct {
	Metadata   OpenAIClientSessionIdentityMetadata
	BlockKey   string
	LookupKeys []string
	Inherited  bool
}

func (r CyberSessionIdentityResolution) Resolved() bool {
	return r.Metadata.Status == OpenAIClientSessionIdentityResolved && r.BlockKey != ""
}

// InheritCyberSessionIdentity marks an already-resolved identity as inherited
// from the current WebSocket connection. It does not alter the key namespace.
func InheritCyberSessionIdentity(bound CyberSessionIdentityResolution) CyberSessionIdentityResolution {
	if !bound.Resolved() {
		return bound
	}
	bound.Metadata.Source = OpenAIClientSessionIdentitySourceConnection
	bound.Inherited = true
	bound.LookupKeys = append([]string(nil), bound.LookupKeys...)
	return bound
}

// ResolveCyberSessionIdentity derives a typed, API-key-scoped hash and lookup
// keys without exposing the raw client identity.
func ResolveCyberSessionIdentity(apiKeyID int64, c *gin.Context, body []byte) CyberSessionIdentityResolution {
	resolution := resolveOpenAIClientSessionIdentity(c, body)
	result := CyberSessionIdentityResolution{Metadata: resolution.metadata}
	if apiKeyID <= 0 || resolution.metadata.Status != OpenAIClientSessionIdentityResolved {
		return result
	}
	result.BlockKey = cyberSessionExplicitBlockKeyForIdentity(apiKeyID, resolution.identity)
	result.LookupKeys = cyberSessionExplicitBlockLookupKeysForIdentity(apiKeyID, resolution.identity)
	return result
}

// CyberSessionExplicitBlockKey accepts explicit conversation-scoped headers or
// client_metadata only. Cache/affinity hints and transcript similarity are not
// session identities. API keys define the authenticated namespace; relays
// sharing a key must supply a distinct thread/session ID for each downstream
// conversation.
func CyberSessionExplicitBlockKey(apiKeyID int64, c *gin.Context, body []byte) string {
	return ResolveCyberSessionIdentity(apiKeyID, c, body).BlockKey
}

func cyberSessionExplicitBlockKeyForIdentity(apiKeyID int64, identity openAIClientSessionIdentity) string {
	raw := "cyber-explicit-session:v3|api_key=" + strconv.FormatInt(apiKeyID, 10) + "|kind=" + identity.kind + "|id=" + identity.value
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func cyberSessionExplicitBlockLookupKeysForIdentity(apiKeyID int64, identity openAIClientSessionIdentity) []string {
	if apiKeyID <= 0 || identity.value == "" {
		return nil
	}
	key := cyberSessionExplicitBlockKeyForIdentity(apiKeyID, identity)

	// Read the former untyped v2 key until its configured TTL naturally
	// expires. New writes use only v3 so thread and session namespaces cannot
	// collide after this version.
	legacyRaw := "cyber-explicit-session:v2|api_key=" + strconv.FormatInt(apiKeyID, 10) + "|session=" + identity.value
	legacySum := sha256.Sum256([]byte(legacyRaw))
	return []string{key, hex.EncodeToString(legacySum[:])}
}

// cyberSessionBlockStore 探测 cache 是否具备屏蔽存储能力。
// 注意：若未来以装饰器包装 GatewayCache（如日志/指标装饰器），该装饰器必须同时实现
// CyberSessionBlockStore，否则会话屏蔽能力将静默降级关闭
// （编译断言 var _ service.CyberSessionBlockStore = (*gatewayCache)(nil) 只覆盖
// *gatewayCache 本体，无法覆盖其外层包装）。
func (s *OpenAIGatewayService) cyberSessionBlockStore() CyberSessionBlockStore {
	if s == nil || s.cache == nil {
		return nil
	}
	store, ok := s.cache.(CyberSessionBlockStore)
	if !ok {
		return nil
	}
	return store
}

// CyberSessionBlockRuntime 返回 (开关, TTL)。开关默认关。
// 委托给 SettingService.GetCyberSessionBlockRuntime，进程内缓存避免热路径 DB 往返。
func (s *OpenAIGatewayService) CyberSessionBlockRuntime(ctx context.Context) (bool, time.Duration) {
	if s == nil || s.settingService == nil {
		return false, time.Hour
	}
	return s.settingService.GetCyberSessionBlockRuntime(ctx)
}

// CyberSessionIdentityStrictEnabled reports whether requests without a
// trustworthy explicit identity must be rejected. The setting is default-off
// and only takes effect while cyber session blocking itself is enabled.
func (s *OpenAIGatewayService) CyberSessionIdentityStrictEnabled(ctx context.Context) bool {
	if s == nil || s.settingService == nil {
		return false
	}
	return s.settingService.GetCyberSessionIdentityStrictEnabled(ctx)
}

// cyberRequestFingerprintFields is the moderation-relevant subset used when a
// client does not provide a trustworthy explicit session identity. Transport
// and cache hints are deliberately excluded so an identical retry still
// matches after a prompt_cache_key or stream flag changes.
var cyberRequestFingerprintFields = []string{
	"messages", "input", "instructions", "system", "prompt",
	"tools", "tool_choice", "previous_response_id", "conversation",
	"thread_id", "session_id", "text",
}

// CyberRequestFingerprintKey returns an API-key-scoped block key for the
// visible request content. It is used only as a fallback when no explicit
// session identity is available: an exact retry of an already blocked request
// must not be sent upstream again. The raw content is never stored; only the
// one-way hash is used as a Redis key.
func CyberRequestFingerprintKey(apiKeyID int64, body []byte) string {
	if apiKeyID <= 0 || len(body) == 0 {
		return ""
	}
	var normalized strings.Builder
	for _, field := range cyberRequestFingerprintFields {
		value := gjson.GetBytes(body, field)
		if !value.Exists() {
			continue
		}
		_, _ = normalized.WriteString(field)
		_ = normalized.WriteByte('=')
		_, _ = normalized.WriteString(value.Raw)
		_ = normalized.WriteByte('\n')
	}
	if normalized.Len() == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte("cyber-request-fingerprint:v1|api_key=" + strconv.FormatInt(apiKeyID, 10) + "|" + normalized.String()))
	return hex.EncodeToString(sum[:])
}

// MarkCyberSessionBlocked 把会话写入屏蔽表（写入点：cyber 命中后）。
// 开关关闭、key 为空或存储不可用时静默跳过。
func (s *OpenAIGatewayService) MarkCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string) {
	if s == nil || len(keys) == 0 {
		return
	}
	enabled, ttl := s.CyberSessionBlockRuntime(ctx)
	if !enabled {
		return
	}
	store := s.cyberSessionBlockStore()
	if store == nil {
		return
	}
	if err := store.SetCyberSessionBlocked(ctx, scopeKey, keys, ttl); err != nil {
		logger.LegacyPrintf("service.openai_gateway", "cyber session block write failed: err=%v", err)
	}
}

// MarkCyberSessionBlockedForRequest prefers the explicit session identity and
// falls back to the content fingerprint when the client supplied none. This is
// what prevents an identical retry from reaching upstream again after a
// cyber_policy hit.
func (s *OpenAIGatewayService) MarkCyberSessionBlockedForRequest(ctx context.Context, apiKeyID int64, identity CyberSessionIdentityResolution, body []byte) {
	keys := make([]string, 0, 1)
	if identity.BlockKey != "" {
		keys = append(keys, identity.BlockKey)
	} else if key := CyberRequestFingerprintKey(apiKeyID, body); key != "" {
		keys = append(keys, key)
	}
	s.MarkCyberSessionBlocked(ctx, "", keys)
}

// FindCyberSessionBlockedForRequest blocks an exact session identity, or an
// exact content fingerprint when the client has no explicit identity. Unknown
// content and storage failures remain fail-open.
func (s *OpenAIGatewayService) FindCyberSessionBlockedForRequest(ctx context.Context, apiKeyID int64, c *gin.Context, body []byte, _ string, _ string) string {
	identity := ResolveCyberSessionIdentity(apiKeyID, c, body)
	return s.findCyberSessionBlocked(ctx, apiKeyID, identity, body)
}

// FindCyberSessionBlockedForIdentityAndBody is used by WebSocket turns, which
// may inherit a connection-bound identity while still needing the per-turn
// fingerprint fallback.
func (s *OpenAIGatewayService) FindCyberSessionBlockedForIdentityAndBody(ctx context.Context, apiKeyID int64, identity CyberSessionIdentityResolution, body []byte) string {
	return s.findCyberSessionBlocked(ctx, apiKeyID, identity, body)
}

// FindCyberSessionBlockedForIdentity checks a pre-resolved identity without a
// request body. Callers that have the body should use the AndBody variant so
// the fingerprint fallback is available.
func (s *OpenAIGatewayService) FindCyberSessionBlockedForIdentity(ctx context.Context, identity CyberSessionIdentityResolution) string {
	return s.findCyberSessionBlocked(ctx, 0, identity, nil)
}

func (s *OpenAIGatewayService) findCyberSessionBlocked(ctx context.Context, apiKeyID int64, identity CyberSessionIdentityResolution, body []byte) string {
	enabled, _ := s.CyberSessionBlockRuntime(ctx)
	if !enabled {
		return ""
	}
	keys := identity.LookupKeys
	fingerprintKey := ""
	if apiKeyID > 0 {
		fingerprintKey = CyberRequestFingerprintKey(apiKeyID, body)
		if fingerprintKey != "" {
			duplicate := false
			for _, key := range keys {
				if key == fingerprintKey {
					duplicate = true
					break
				}
			}
			if !duplicate {
				keys = append(keys, fingerprintKey)
			}
		}
	}
	if len(keys) == 0 {
		return ""
	}
	store := s.cyberSessionBlockStore()
	if store == nil {
		return ""
	}
	matched, err := store.FindCyberSessionBlocked(ctx, keys)
	if err != nil {
		logger.LegacyPrintf("service.openai_gateway", "cyber session block read failed: err=%v", err)
		return ""
	}
	if matched != "" && matched == fingerprintKey {
		logger.LegacyPrintf("service.openai_gateway", "cyber request fingerprint block matched: api_key_id=%d", apiKeyID)
	}
	return matched
}
