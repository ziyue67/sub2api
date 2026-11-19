package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// openAIGPT61SolSubscriptionLongContextThreshold 是订阅凭据的长上下文门槛。
// API 价卡仍是 272,000；订阅账号在该值及以下不套用 272k 附加费，超过才算长上下文。
const openAIGPT61SolSubscriptionLongContextThreshold = 500_000

// openAISubscriptionLongContextExempt 按实际上游凭据、最终发送模型和输入 token
// 判断是否豁免官方模型价卡的长上下文附加费。
//
// OpenAI 订阅凭据（OAuth、Setup Token）上的 GPT-6.2 和 Terra 不收取该附加费。
// GPT-6.1 Sol 仅在计入长上下文判断的输入 token 不超过 500,000 时豁免；
// 超过 500,000 时沿用价卡倍率（输入 ×2、输出 ×1.5，缓存侧与价卡一致）。
// API Key 仍按各模型价卡的阶梯；其它平台和 upstream 透传不豁免。
//
// 该标志只关闭官方模型长上下文阶梯（DisableModelLongContextPricing），
// 分组自定义区间价格不受影响。token 口径与 shouldApplySessionLongContextPricing
// 相同：普通输入 + 缓存创建 + 缓存读取。
//
// 使用发送模型（UpstreamModel，空则回退 Model），避免客户别名、渠道计费模型
// 或响应模型计费切换改变规则。账号统计成本与用户扣费共用这次判断。
func openAISubscriptionLongContextExempt(account *Account, result *OpenAIForwardResult, tokens UsageTokens) bool {
	if result == nil || !account.IsOpenAIOAuthLike() {
		return false
	}
	model := strings.TrimSpace(result.UpstreamModel)
	if model == "" {
		model = result.Model
	}
	// GPT-6.2 and Terra follow the upstream subscription exemption; keep the fork's Sol 500k boundary.
	if openai.GPT62ModelID(model) != "" || openai.IsGPT61TerraModelSpelling(model) {
		return true
	}
	if !openai.IsGPT61SolModelSpelling(model) {
		return false
	}
	return longContextBillableInputTokens(tokens) <= openAIGPT61SolSubscriptionLongContextThreshold
}
