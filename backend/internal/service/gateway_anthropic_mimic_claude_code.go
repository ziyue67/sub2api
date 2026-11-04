package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Anthropic API Key 账号的「模拟 Claude Code 客户端」：上游（例如开了"仅允许 Claude Code"
// 的 sub2api 分组）只放行 Claude Code 时，给普通客户端的请求补齐 ClaudeCodeValidator
// 检查的那几项特征——claude-cli User-Agent、x-app、anthropic-beta、system 数组里的身份句、
// 合法的 metadata.user_id。只补缺的：真实 Claude Code 请求原样通过，也不扣任何 token。
// 补进 system 的身份句会让上游多计输入 token，仅管理员开启估算扣减时才在记账时扣回。

const claudeCodeMimicInjectionKey = "anthropic_claude_code_mimic_injection"

// 身份句补在 system 最前面多出的输入 token 估算，基于 2026-10-31 的 count_tokens 样本：
// Opus 4.7 起的新分词器补在已有 system 前多 23 个、原本没有 system 时多 24 个；
// 更早的型号是 14 / 15（4.5 系在无 system、无工具时样本为 14，统一按 15 扣）。
// 未识别型号沿用新分词器估算；上游分词器和内容边界可能改变实际增量。
const (
	claudeCodeIdentityTokens       = 23
	claudeCodeIdentityLegacyTokens = 14
)

// claudeCodeMimicInjection 记录一次转发在 system 前补的身份句，记账时据此扣回。
type claudeCodeMimicInjection struct {
	AccountID int64
	// Tokens 是补身份句多出的输入 token，0 表示这次没有补。
	Tokens int
	// Cached 表示身份句之后还有缓存断点：这些 token 跟着整段前缀进了缓存读取或缓存写入。
	Cached bool
	// CacheTTL1h 表示身份句之后第一个缓存断点是 1 小时缓存。
	CacheTTL1h bool
}

func claudeCodeIdentityExtraTokens(model string, clientHasSystem bool) int {
	tokens := claudeCodeIdentityTokens
	if claude.UsesLegacyTokenizer(model) {
		tokens = claudeCodeIdentityLegacyTokens
	}
	if !clientHasSystem {
		tokens++
	}
	return tokens
}

// mimicAnthropicClaudeCodeBody 只补缺的 Claude Code 请求体特征：
//   - system 是字符串时改成同内容的单个文本块（ClaudeCodeValidator 只认数组，token 数不变）；
//   - system 里没有 Claude Code 身份句时，在最前面补一句（不带 cache_control）；
//   - metadata.user_id 缺失或格式不对时，生成会话级稳定的值，格式跟随 userAgent 的 CLI 版本。
func mimicAnthropicClaudeCodeBody(body []byte, account *Account, apiKeyID int64, userAgent, model string) ([]byte, claudeCodeMimicInjection) {
	var injection claudeCodeMimicInjection
	if account != nil {
		injection.AccountID = account.ID
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body, injection
	}
	out := body

	sys := gjson.GetBytes(out, "system")
	if !sys.Exists() || sys.Type == gjson.Null || sys.Type == gjson.String || sys.IsArray() {
		blocks := make([][]byte, 0, 4)
		rewrite := false
		switch {
		case sys.Type == gjson.String:
			rewrite = true
			if text := sys.String(); strings.TrimSpace(text) != "" {
				if block, err := marshalAnthropicSystemTextBlock(text, false); err == nil {
					blocks = append(blocks, block)
				}
			}
		case sys.IsArray():
			for _, item := range sys.Array() {
				blocks = append(blocks, []byte(item.Raw))
			}
		}
		clientHasSystem := len(blocks) > 0
		if !systemBlocksHaveClaudeCodeIdentity(blocks) {
			if identity, err := marshalAnthropicSystemTextBlock(claudeCodeSystemPrompt, false); err == nil {
				blocks = append([][]byte{identity}, blocks...)
				injection.Tokens = claudeCodeIdentityExtraTokens(model, clientHasSystem)
				rewrite = true
			}
		}
		if rewrite {
			if next, ok := setJSONRawBytes(out, "system", buildJSONArrayRaw(blocks)); ok {
				out = next
			} else {
				injection.Tokens = 0
			}
		}
	}
	if injection.Tokens > 0 {
		injection.Cached, injection.CacheTTL1h = claudeCodeMimicCacheBreakpoint(out)
	}

	if ParseMetadataUserID(gjson.GetBytes(out, "metadata.user_id").String()) == nil {
		userID := claudeCodeMimicMetadataUserID(account, apiKeyID, userAgent, out)
		if meta := gjson.GetBytes(out, "metadata"); meta.Exists() && !meta.IsObject() {
			if raw, err := marshalAnthropicMetadata(userID); err == nil {
				if next, ok := setJSONRawBytes(out, "metadata", raw); ok {
					out = next
				}
			}
		} else if next, ok := setJSONValueBytes(out, "metadata.user_id", userID); ok {
			out = next
		}
	}
	return out, injection
}

// systemBlocksHaveClaudeCodeIdentity 复用 ClaudeCodeValidator 的判定口径（身份句相似度、
// 计费归因块、安全监视器提示词），与上游 sub2api 的"仅允许 Claude Code"检查保持一致。
func systemBlocksHaveClaudeCodeIdentity(blocks [][]byte) bool {
	if len(blocks) == 0 {
		return false
	}
	var entries []any
	if err := json.Unmarshal(buildJSONArrayRaw(blocks), &entries); err != nil {
		return false
	}
	return NewClaudeCodeValidator().hasClaudeCodeSystemPrompt(map[string]any{"model": "", "system": entries})
}

// claudeCodeMimicCacheBreakpoint 判断补在 system 最前面的身份句会不会进缓存。缓存前缀的
// 顺序是 tools → system → messages，只有 system、messages 上（或顶层自动缓存）的断点
// 才盖得到它；第一个这样的断点决定缓存时长。
func claudeCodeMimicCacheBreakpoint(body []byte) (cached bool, ttl1h bool) {
	breakpoint := func(cc gjson.Result) bool {
		if !cc.Exists() || !cc.IsObject() {
			return false
		}
		cached, ttl1h = true, cc.Get("ttl").String() == "1h"
		return true
	}
	for _, block := range gjson.GetBytes(body, "system").Array() {
		if breakpoint(block.Get("cache_control")) {
			return cached, ttl1h
		}
	}
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		content := message.Get("content")
		if !content.IsArray() {
			continue
		}
		for _, block := range content.Array() {
			if breakpoint(block.Get("cache_control")) {
				return cached, ttl1h
			}
		}
	}
	breakpoint(gjson.GetBytes(body, "cache_control"))
	return cached, ttl1h
}

// claudeCodeMimicMetadataUserID 生成补给上游的 metadata.user_id：设备号按账号固定，
// 会话号用"账号 + 调用方 API Key + 首条 user 文本"派生，同一段对话追加消息时保持不变，
// 让上游的粘性会话和缓存继续命中。
func claudeCodeMimicMetadataUserID(account *Account, apiKeyID int64, userAgent string, body []byte) string {
	var accountID int64
	if account != nil {
		accountID = account.ID
	}
	device := sha256.Sum256([]byte("sub2api:anthropic-claude-code-mimic:" + strconv.FormatInt(accountID, 10)))
	seed := buildStableSessionSeed(accountID, strconv.FormatInt(apiKeyID, 10), extractFirstUserText(body))
	return FormatMetadataUserID(hex.EncodeToString(device[:]), "", generateSessionUUID(seed), ExtractCLIVersion(userAgent))
}

// applyAnthropicClaudeCodeMimicHeaders 只补缺的 Claude Code 请求头：不是 claude-cli 的
// User-Agent 换成 userAgent，x-app 缺失时补 cli，anthropic-beta 里补上 claude-code beta。
func applyAnthropicClaudeCodeMimicHeaders(h http.Header, userAgent string) {
	if h == nil {
		return
	}
	if !claudeCodeUAPattern.MatchString(strings.TrimSpace(getHeaderRaw(h, "User-Agent"))) {
		setHeaderRaw(h, resolveWireCasing("User-Agent"), userAgent)
	}
	if strings.TrimSpace(getHeaderRaw(h, "x-app")) == "" {
		setHeaderRaw(h, resolveWireCasing("x-app"), "cli")
	}
	beta := withClaudeCodeBetaToken(getHeaderRaw(h, "anthropic-beta"))
	deleteHeaderAllForms(h, "anthropic-beta")
	setHeaderRaw(h, resolveWireCasing("anthropic-beta"), beta)
	if getHeaderRaw(h, "anthropic-version") == "" {
		setHeaderRaw(h, "anthropic-version", "2023-06-01")
	}
}

// withClaudeCodeBetaToken 在 anthropic-beta 列表最前面补上 claude-code beta，已有就保持原样。
func withClaudeCodeBetaToken(header string) string {
	tokens := make([]string, 0, 4)
	for _, part := range strings.Split(header, ",") {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		if strings.EqualFold(token, claude.BetaClaudeCode) {
			return header
		}
		tokens = append(tokens, token)
	}
	tokens = append([]string{claude.BetaClaudeCode}, tokens...)
	return strings.Join(tokens, ",")
}

// claudeCodeMimicAPIKeyID 取调用方 API Key 的 ID，用来区分不同用户的相同首条消息。
func claudeCodeMimicAPIKeyID(c *gin.Context) int64 {
	if c == nil {
		return 0
	}
	if apiKey := getAPIKeyFromContext(c); apiKey != nil {
		return apiKey.ID
	}
	return 0
}

// setClaudeCodeMimicInjection 每次构造上游请求都写一次（没补就是 0），换号重试时不会
// 留下上一个账号的记录。
func setClaudeCodeMimicInjection(c *gin.Context, injection claudeCodeMimicInjection) {
	if c != nil {
		c.Set(claudeCodeMimicInjectionKey, injection)
	}
}

// attachClaudeCodeMimicInjection 把本次转发补身份句的记录挂到转发结果上，只认同一账号写下的记录。
func attachClaudeCodeMimicInjection(c *gin.Context, account *Account, result *ForwardResult) {
	if c == nil || result == nil || !account.IsAnthropicMimicClaudeCodeDeductTokensEnabled() {
		return
	}
	value, ok := c.Get(claudeCodeMimicInjectionKey)
	if !ok {
		return
	}
	injection, ok := value.(claudeCodeMimicInjection)
	if !ok || injection.AccountID != account.ID || injection.Tokens <= 0 {
		return
	}
	result.claudeCodeMimic = injection
}

// deductClaudeCodeMimicUsage 从用量里扣回补的身份句 token 并清掉记录，同一结果重复记账时
// 不会扣两次。身份句之后有缓存断点时，它跟着整段前缀进了缓存（命中算缓存读取、未命中算
// 缓存写入），否则算普通输入。上游只提供汇总用量，tools 单独命中或混合 TTL 时
// 无法精确定位身份句所属的缓存段，这里的分类为估算。返回实际扣掉的 token 数。
func deductClaudeCodeMimicUsage(result *ForwardResult) int {
	if result == nil {
		return 0
	}
	injection := result.claudeCodeMimic
	result.claudeCodeMimic = claudeCodeMimicInjection{}
	tokens := injection.Tokens
	if tokens <= 0 {
		return 0
	}
	usage := &result.Usage
	if injection.Cached {
		if usage.CacheReadInputTokens >= tokens {
			usage.CacheReadInputTokens -= tokens
			return tokens
		}
		if usage.CacheCreationInputTokens >= tokens {
			usage.CacheCreationInputTokens -= tokens
			first, second := &usage.CacheCreation5mTokens, &usage.CacheCreation1hTokens
			if injection.CacheTTL1h {
				first, second = second, first
			}
			rest := takeTokens(first, tokens)
			takeTokens(second, rest)
			return tokens
		}
	}
	return tokens - takeTokens(&usage.InputTokens, tokens)
}

// takeTokens 从 bucket 里最多扣 n 个，返回没扣完的部分。
func takeTokens(bucket *int, n int) int {
	if *bucket <= 0 {
		return n
	}
	if *bucket >= n {
		*bucket -= n
		return 0
	}
	rest := n - *bucket
	*bucket = 0
	return rest
}
