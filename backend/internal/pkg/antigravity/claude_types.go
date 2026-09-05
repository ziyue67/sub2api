package antigravity

import (
	"encoding/json"
	"strings"
)

// Claude 请求/响应类型定义

// ClaudeRequest Claude Messages API 请求
type ClaudeRequest struct {
	Model       string          `json:"model"`
	Messages    []ClaudeMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	System      json.RawMessage `json:"system,omitempty"` // string 或 []SystemBlock
	Stream      bool            `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	TopK        *int            `json:"top_k,omitempty"`
	Tools       []ClaudeTool    `json:"tools,omitempty"`
	Thinking    *ThinkingConfig `json:"thinking,omitempty"`
	Metadata    *ClaudeMetadata `json:"metadata,omitempty"`
}

// ClaudeMessage Claude 消息
type ClaudeMessage struct {
	Role    string          `json:"role"` // user, assistant
	Content json.RawMessage `json:"content"`
}

// ThinkingConfig Thinking 配置
type ThinkingConfig struct {
	Type         string `json:"type"`                    // "enabled" / "adaptive" / "disabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"` // thinking budget
}

// ClaudeMetadata 请求元数据
type ClaudeMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

// ClaudeTool Claude 工具定义
// 支持两种格式：
// 1. 标准格式: { "name": "...", "description": "...", "input_schema": {...} }
// 2. Custom 格式 (MCP): { "type": "custom", "name": "...", "custom": { "description": "...", "input_schema": {...} } }
type ClaudeTool struct {
	Type        string          `json:"type,omitempty"` // "custom" 或空（标准格式）
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`  // 标准格式使用
	InputSchema map[string]any  `json:"input_schema,omitempty"` // 标准格式使用
	Custom      *CustomToolSpec `json:"custom,omitempty"`       // custom 格式使用
}

// CustomToolSpec MCP custom 工具规格
type CustomToolSpec struct {
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// ClaudeCustomToolSpec 兼容旧命名（MCP custom 工具规格）
type ClaudeCustomToolSpec = CustomToolSpec

// SystemBlock system prompt 数组形式的元素
type SystemBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ContentBlock Claude 消息内容块（解析后）
type ContentBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	// tool_use
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
	// tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	// image
	Source *ImageSource `json:"source,omitempty"`
}

// ImageSource Claude 图片来源
type ImageSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // "image/png", "image/jpeg" 等
	Data      string `json:"data"`
}

// ClaudeResponse Claude Messages API 响应
type ClaudeResponse struct {
	ID           string              `json:"id"`
	Type         string              `json:"type"` // "message"
	Role         string              `json:"role"` // "assistant"
	Model        string              `json:"model"`
	Content      []ClaudeContentItem `json:"content"`
	StopReason   string              `json:"stop_reason,omitempty"`   // end_turn, tool_use, max_tokens
	StopSequence *string             `json:"stop_sequence,omitempty"` // null 或具体值
	Usage        ClaudeUsage         `json:"usage"`
}

// ClaudeContentItem Claude 响应内容项
type ClaudeContentItem struct {
	Type string `json:"type"` // text, thinking, tool_use

	// text
	Text string `json:"text,omitempty"`

	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// tool_use
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
}

// ClaudeUsage Claude 用量统计
type ClaudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	ImageOutputTokens        int `json:"image_output_tokens,omitempty"`
}

// ClaudeError Claude 错误响应
type ClaudeError struct {
	Type  string      `json:"type"` // "error"
	Error ErrorDetail `json:"error"`
}

// ErrorDetail 错误详情
type ErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// modelDef Antigravity 模型定义（内部使用）
type modelDef struct {
	ID          string
	DisplayName string
	CreatedAt   string // 仅 Claude API 格式使用
	IsReasoning bool
}

// Antigravity 支持的 Claude 模型
var claudeModels = []modelDef{
	{ID: "claude-fable-5-1", DisplayName: "Claude Fable 5.1", CreatedAt: "2026-09-01T00:00:00Z"},
	{ID: "claude-fable-5", DisplayName: "Claude Fable 5", CreatedAt: "2026-06-09T00:00:00Z"},
	{ID: "claude-opus-4-5-thinking", DisplayName: "Claude Opus 4.5 Thinking", CreatedAt: "2025-11-01T00:00:00Z"},
	{ID: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5", CreatedAt: "2025-09-29T00:00:00Z"},
	{ID: "claude-sonnet-4-5-thinking", DisplayName: "Claude Sonnet 4.5 Thinking", CreatedAt: "2025-09-29T00:00:00Z"},
	{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6", CreatedAt: "2026-02-05T00:00:00Z"},
	{ID: "claude-opus-4-6-thinking", DisplayName: "Claude Opus 4.6 Thinking", CreatedAt: "2026-02-05T00:00:00Z"},
	{ID: "claude-opus-4-7", DisplayName: "Claude Opus 4.7", CreatedAt: "2026-04-17T00:00:00Z"},
	{ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8", CreatedAt: "2026-05-29T00:00:00Z"},
	{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", CreatedAt: "2026-02-17T00:00:00Z"},
}

// Antigravity 支持的 Gemini 模型
var geminiModels = []modelDef{
	{ID: "gemini-2.5-flash", DisplayName: "Gemini 2.5 Flash", CreatedAt: "2025-01-01T00:00:00Z"},
	{ID: "gemini-2.5-flash-image", DisplayName: "Gemini 2.5 Flash Image", CreatedAt: "2025-01-01T00:00:00Z"},
	{ID: "gemini-2.5-flash-image-preview", DisplayName: "Gemini 2.5 Flash Image Preview", CreatedAt: "2025-01-01T00:00:00Z"},
	{ID: "gemini-2.5-flash-lite", DisplayName: "Gemini 2.5 Flash Lite", CreatedAt: "2025-01-01T00:00:00Z"},
	{ID: "gemini-2.5-flash-thinking", DisplayName: "Gemini 2.5 Flash Thinking", CreatedAt: "2025-01-01T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3-flash", DisplayName: "Gemini 3 Flash", CreatedAt: "2025-06-01T00:00:00Z"},
	{ID: "gemini-3-pro-low", DisplayName: "Gemini 3 Pro Low", CreatedAt: "2025-06-01T00:00:00Z"},
	{ID: "gemini-3-pro-high", DisplayName: "Gemini 3 Pro High", CreatedAt: "2025-06-01T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.1-pro-low", DisplayName: "Gemini 3.1 Pro Low", CreatedAt: "2026-02-19T00:00:00Z"},
	{ID: "gemini-3.1-pro-high", DisplayName: "Gemini 3.1 Pro High", CreatedAt: "2026-02-19T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.1-flash-image", DisplayName: "Gemini 3.1 Flash Image", CreatedAt: "2026-02-19T00:00:00Z"},
	{ID: "gemini-3.1-flash-image-preview", DisplayName: "Gemini 3.1 Flash Image Preview", CreatedAt: "2026-02-19T00:00:00Z"},
	{ID: "gemini-3.6-flash", DisplayName: "Gemini 3.6 Flash", CreatedAt: "2026-07-21T00:00:00Z"},
	{ID: "gemini-3.6-flash-high", DisplayName: "Gemini 3.6 Flash High", CreatedAt: "2026-07-21T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.6-flash-low", DisplayName: "Gemini 3.6 Flash Low", CreatedAt: "2026-07-21T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.6-flash-medium", DisplayName: "Gemini 3.6 Flash Medium", CreatedAt: "2026-07-21T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.6-flash-tiered", DisplayName: "Gemini 3.6 Flash", CreatedAt: "2026-07-21T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.7-flash", DisplayName: "Gemini 3.7 Flash", CreatedAt: "2026-08-13T00:00:00Z"},
	{ID: "gemini-3.7-flash-high", DisplayName: "Gemini 3.7 Flash High", CreatedAt: "2026-08-13T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.7-flash-low", DisplayName: "Gemini 3.7 Flash Low", CreatedAt: "2026-08-13T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.7-flash-medium", DisplayName: "Gemini 3.7 Flash Medium", CreatedAt: "2026-08-13T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.7-flash-tiered", DisplayName: "Gemini 3.7 Flash", CreatedAt: "2026-08-13T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.8-flash", DisplayName: "Gemini 3.8 Flash", CreatedAt: "2026-09-02T00:00:00Z"},
	{ID: "gemini-3.8-flash-high", DisplayName: "Gemini 3.8 Flash High", CreatedAt: "2026-09-02T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.8-flash-low", DisplayName: "Gemini 3.8 Flash Low", CreatedAt: "2026-09-02T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.8-flash-medium", DisplayName: "Gemini 3.8 Flash Medium", CreatedAt: "2026-09-02T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.8-flash-tiered", DisplayName: "Gemini 3.8 Flash", CreatedAt: "2026-09-02T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3-pro-preview", DisplayName: "Gemini 3 Pro Preview", CreatedAt: "2025-06-01T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3-pro-image", DisplayName: "Gemini 3 Pro Image", CreatedAt: "2025-06-01T00:00:00Z"},
}

// ========== Claude API 格式 (/v1/models) ==========

// ClaudeModel Claude API 模型格式
type ClaudeModel struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// DefaultModels 返回 Claude API 格式的模型列表（Claude + Gemini）
func DefaultModels() []ClaudeModel {
	all := append(claudeModels, geminiModels...)
	result := make([]ClaudeModel, len(all))
	for i, m := range all {
		result[i] = ClaudeModel{ID: m.ID, Type: "model", DisplayName: m.DisplayName, CreatedAt: m.CreatedAt}
	}
	return result
}

// ========== Gemini v1beta 格式 (/v1beta/models) ==========

// GeminiModel Gemini v1beta 模型格式
type GeminiModel struct {
	Name                       string   `json:"name"`
	DisplayName                string   `json:"displayName,omitempty"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods,omitempty"`
}

// GeminiModelsListResponse Gemini v1beta 模型列表响应
type GeminiModelsListResponse struct {
	Models []GeminiModel `json:"models"`
}

var defaultGeminiMethods = []string{"generateContent", "streamGenerateContent"}

// DefaultGeminiModels 返回 Gemini v1beta 格式的模型列表（仅 Gemini 模型）
func DefaultGeminiModels() []GeminiModel {
	result := make([]GeminiModel, len(geminiModels))
	for i, m := range geminiModels {
		result[i] = GeminiModel{Name: "models/" + m.ID, DisplayName: m.DisplayName, SupportedGenerationMethods: defaultGeminiMethods}
	}
	return result
}

// FallbackGeminiModelsList 返回 Gemini v1beta 格式的模型列表响应
func FallbackGeminiModelsList() GeminiModelsListResponse {
	return GeminiModelsListResponse{Models: DefaultGeminiModels()}
}

// FallbackGeminiModel 返回单个模型信息（v1beta 格式）
func FallbackGeminiModel(model string) GeminiModel {
	if model == "" {
		return GeminiModel{Name: "models/unknown", SupportedGenerationMethods: defaultGeminiMethods}
	}
	name := model
	if len(model) < 7 || model[:7] != "models/" {
		name = "models/" + model
	}
	return GeminiModel{Name: name, SupportedGenerationMethods: defaultGeminiMethods}
}

// geminiFlashTierBaseModels 列出仅由 Antigravity 提供、公共 Gemini API
// （AI Studio / Gemini CLI / Code Assist）并不存在的 Flash 分档系列基座。
//
// 这些 ID 只在 Antigravity 上游可用，因此不能按 "gemini-" 前缀归类到 gemini
// 平台：composite 分组一旦把它们解析成 gemini，调度阶段就会跳过所有
// antigravity 账号，客户端只能拿到 400/404「不支持该模型」。
var geminiFlashTierBaseModels = []string{
	"gemini-3.6-flash",
	"gemini-3.7-flash",
	"gemini-3.8-flash",
}

// geminiFlashTierSuffixes 是上述基座模型的推理分档后缀（空串表示基座本身）。
var geminiFlashTierSuffixes = []string{"", "-high", "-low", "-medium", "-tiered"}

// IsAntigravityOnlyGeminiModel 判断模型是否为「仅 Antigravity 提供」的 Gemini 模型。
// 采用精确白名单而非前缀匹配：gemini-2.5-*/gemini-3-flash 等同时存在于公共
// Gemini 通道的模型必须保持原有平台归属，避免改动既有部署的路由语义。
func IsAntigravityOnlyGeminiModel(modelID string) bool {
	normalized := strings.ToLower(strings.TrimSpace(modelID))
	normalized = strings.TrimPrefix(normalized, "models/")
	if normalized == "" {
		return false
	}
	for _, base := range geminiFlashTierBaseModels {
		for _, suffix := range geminiFlashTierSuffixes {
			if normalized == base+suffix {
				return true
			}
		}
	}
	return false
}

// AntigravityOnlyGeminiModels 返回全部「仅 Antigravity 提供」的 Gemini 模型 ID，
// 供后端注册表与测试共享同一份清单。
func AntigravityOnlyGeminiModels() []string {
	out := make([]string, 0, len(geminiFlashTierBaseModels)*len(geminiFlashTierSuffixes))
	for _, base := range geminiFlashTierBaseModels {
		for _, suffix := range geminiFlashTierSuffixes {
			out = append(out, base+suffix)
		}
	}
	return out
}

// IsGeminiReasoningModel 判断是否为不支持参数和强制 ToolConfig 的 Gemini 推理模型
func IsGeminiReasoningModel(modelID string) bool {
	lowerID := strings.ToLower(modelID)
	for _, m := range geminiModels {
		if strings.Contains(lowerID, m.ID) && m.IsReasoning {
			return true
		}
	}
	return false
}
