package service

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	defaultAnthropicAPIKeyNeutralUserAgent = "Anthropic/JS 0.94.0"
	neutralCodingAssistantSystemPrompt     = "You are a helpful AI coding assistant."
	neutralFileSearchSystemPrompt          = "You are a file search specialist for an AI coding assistant."
)

var (
	claudeCodeBillingHeaderLineRe = regexp.MustCompile(`(?im)^[ \t]*x-anthropic-billing-header:[^\r\n]*(?:\r?\n)?`)
	claudeCodeCompactAgentSDKRe   = regexp.MustCompile(`(?i)You are Claude Code,\s*Anthropic['’]s official CLI for Claude,\s*running within the Claude Agent SDK\.?`)
	claudeCodeFileSearchRe        = regexp.MustCompile(`(?i)You are a file search specialist for Claude Code,\s*Anthropic['’]s official CLI for Claude\.?`)
	claudeCodeOfficialCLIRe       = regexp.MustCompile(`(?i)You are Claude Code,\s*Anthropic['’]s official CLI for Claude\.?`)
	claudeCodeAgentSDKRe          = regexp.MustCompile(`(?i)You are a Claude agent,\s*built on Anthropic['’]s Claude Agent SDK\.?`)
)

func isClaudeCodeIdentityBetaToken(token string) bool {
	lower := strings.ToLower(strings.TrimSpace(token))
	if lower == "" {
		return false
	}
	return lower == claude.BetaClaudeCode ||
		lower == claude.BetaOAuth ||
		strings.HasPrefix(lower, "claude-code-") ||
		strings.HasPrefix(lower, "oauth-")
}

// stripAnthropicClaudeCodeIdentityFromBetaHeader 从 anthropic-beta 逗号分隔列表中移除
// Claude Code / OAuth 身份相关的 beta token（如 claude-code-20250219、oauth-2025-04-20），
// 保留其余能力型 beta token（如 interleaved-thinking、fine-grained-tool-streaming、effort 等）。
func stripAnthropicClaudeCodeIdentityFromBetaHeader(header string) string {
	if strings.TrimSpace(header) == "" {
		return ""
	}
	parts := strings.Split(header, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" || isClaudeCodeIdentityBetaToken(token) {
			continue
		}
		out = append(out, token)
	}
	return strings.Join(out, ",")
}

func isClaudeCodeIdentityUserAgent(ua string) bool {
	lower := strings.ToLower(strings.TrimSpace(ua))
	if lower == "" {
		return true
	}
	return strings.Contains(lower, "claude-cli") || strings.Contains(lower, "claude-code")
}

// stripAnthropicClaudeCodeIdentityFromHeaders 剥离出站请求头中的 Claude Code 客户端身份特征：
//   - 将 claude-cli / claude-code User-Agent（或空 UA）替换为标准 Anthropic SDK User-Agent
//   - 从 anthropic-beta 中剔除 claude-code-* 与 oauth-* token；若无剩余 token 则删除该头
//   - 删除 x-app、x-claude-code-session-id、anthropic-dangerous-direct-browser-access
func stripAnthropicClaudeCodeIdentityFromHeaders(h http.Header) {
	if h == nil {
		return
	}

	ua := getHeaderRaw(h, "User-Agent")
	if isClaudeCodeIdentityUserAgent(ua) {
		setHeaderRaw(h, "User-Agent", defaultAnthropicAPIKeyNeutralUserAgent)
	}

	rawBeta := getHeaderRaw(h, "anthropic-beta")
	deleteHeaderAllForms(h, "anthropic-beta")
	if cleanedBeta := stripAnthropicClaudeCodeIdentityFromBetaHeader(rawBeta); cleanedBeta != "" {
		setHeaderRaw(h, "anthropic-beta", cleanedBeta)
	}

	deleteHeaderAllForms(h, "x-app")
	deleteHeaderAllForms(h, "x-claude-code-session-id")
	deleteHeaderAllForms(h, "anthropic-dangerous-direct-browser-access")
}

func sanitizeAnthropicClaudeCodeSystemText(text string) string {
	if text == "" {
		return text
	}
	out := claudeCodeBillingHeaderLineRe.ReplaceAllString(text, "")
	out = claudeCodeCompactAgentSDKRe.ReplaceAllString(out, neutralCodingAssistantSystemPrompt)
	out = claudeCodeFileSearchRe.ReplaceAllString(out, neutralFileSearchSystemPrompt)
	out = claudeCodeOfficialCLIRe.ReplaceAllString(out, neutralCodingAssistantSystemPrompt)
	out = claudeCodeAgentSDKRe.ReplaceAllString(out, neutralCodingAssistantSystemPrompt)
	return out
}

// stripAnthropicClaudeCodeIdentityFromBody 剥离请求体中的 Claude Code / Agent SDK 身份特征：
//   - 移除 system 数组中以 x-anthropic-billing-header 开头的计费归因块
//   - 将 system 中的 Claude Code / Agent SDK 官方身份句替换为中性助手提示词，保留 cache_control 及其他指令
//   - 移除 metadata.user_id（若 metadata 变为空对象则一并移除）
func stripAnthropicClaudeCodeIdentityFromBody(body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	out := body

	sys := gjson.GetBytes(out, "system")
	if sys.Exists() {
		switch {
		case sys.Type == gjson.String:
			orig := sys.String()
			cleaned := sanitizeAnthropicClaudeCodeSystemText(orig)
			if strings.TrimSpace(cleaned) == "" {
				if next, ok := deleteJSONPathBytes(out, "system"); ok {
					out = next
				}
			} else if cleaned != orig {
				if next, ok := setJSONValueBytes(out, "system", cleaned); ok {
					out = next
				}
			}
		case sys.IsArray():
			items := sys.Array()
			kept := make([][]byte, 0, len(items))
			changed := false
			for _, item := range items {
				if item.IsObject() && item.Get("type").String() == "text" {
					textNode := item.Get("text")
					if textNode.Exists() && textNode.Type == gjson.String {
						origText := textNode.String()
						trimmedLower := strings.ToLower(strings.TrimSpace(origText))
						if strings.HasPrefix(trimmedLower, claudeCodeBillingHeaderPrefix) {
							changed = true
							continue
						}
						cleanedText := sanitizeAnthropicClaudeCodeSystemText(origText)
						if strings.TrimSpace(cleanedText) == "" {
							changed = true
							continue
						}
						if cleanedText != origText {
							updatedBlock, err := sjson.SetBytes([]byte(item.Raw), "text", cleanedText)
							if err == nil {
								kept = append(kept, updatedBlock)
								changed = true
								continue
							}
						}
					}
				}
				kept = append(kept, []byte(item.Raw))
			}
			if changed {
				if len(kept) == 0 {
					if next, ok := deleteJSONPathBytes(out, "system"); ok {
						out = next
					}
				} else if next, ok := setJSONRawBytes(out, "system", buildJSONArrayRaw(kept)); ok {
					out = next
				}
			}
		}
	}

	meta := gjson.GetBytes(out, "metadata")
	if meta.Exists() && meta.IsObject() && meta.Get("user_id").Exists() {
		if next, ok := deleteJSONPathBytes(out, "metadata.user_id"); ok {
			out = next
			if remaining := gjson.GetBytes(out, "metadata"); remaining.Exists() && remaining.IsObject() && len(remaining.Map()) == 0 {
				if next2, ok2 := deleteJSONPathBytes(out, "metadata"); ok2 {
					out = next2
				}
			}
		}
	}

	return out
}
