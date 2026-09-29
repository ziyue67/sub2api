import type { CreateScheduledTestPlanRequest, PelicanTestConfig, QualityBPSPolicy, ScheduledTestPlan, UpdateScheduledTestPlanRequest } from '@/types'
import { DEFAULT_STATE_PROBE_CRON, STATE_PROBE_QUESTION } from './intelligenceTest'
import { defaultQualityBPS, qualityBPSForm, qualityBPSPayload } from './qualityRulePatch'

// 添加/编辑账号弹窗里的「降智后自动开启 BPS」就是一条质量运维规则（状态探针 + 开启 BPS 协议）；
// 新规则默认每 2 分钟检测；已有规则保留各自的检测计划和 BPS 设置。
export const AUTO_BPS_PROBE_MODEL = 'gpt-6-astra'
export const AUTO_BPS_CRON = DEFAULT_STATE_PROBE_CRON

export type AutoBPSDraft = { enabled: boolean; autoRestore: boolean; cronExpression: string; bps: QualityBPSPolicy }

export function newAutoBPSDraft(): AutoBPSDraft {
  return { enabled: false, autoRestore: true, cronExpression: AUTO_BPS_CRON, bps: defaultQualityBPS() }
}

export function isAutoBPSRule(plan: ScheduledTestPlan): boolean {
  return plan.pelican_config?.question_kind === STATE_PROBE_QUESTION && plan.pelican_config.quality?.action === 'enable_bps'
}

// 一个账号有多条时弹窗只管一条：优先正在运行的，其次最早建的。
export function pickAutoBPSRule(plans: ScheduledTestPlan[]): ScheduledTestPlan | null {
  const rules = plans.filter(isAutoBPSRule).sort((a, b) => a.id - b.id)
  return rules.find(plan => plan.enabled) ?? rules[0] ?? null
}

export function autoBPSDraftFromRule(rule: ScheduledTestPlan | null): AutoBPSDraft {
  if (!rule) return newAutoBPSDraft()
  const quality = rule.pelican_config?.quality
  return { enabled: rule.enabled, autoRestore: !!quality?.auto_restore, cronExpression: rule.cron_expression, bps: qualityBPSForm(quality?.bps) }
}

// 与质量运维保存探针规则时的口径一致：探针不带题目和参考答案，只跑一路。
function autoBPSConfig(draft: AutoBPSDraft, base?: PelicanTestConfig): PelicanTestConfig {
  return { reasoning_effort: 'high', ...base, question_kind: STATE_PROBE_QUESTION, prompt: '', parallel_count: 1,
    quality: { expected_answer: '', action: 'enable_bps', remove_group_ids: [], auto_restore: draft.autoRestore, bps: qualityBPSPayload(draft.bps) } }
}

export function autoBPSCreateRequest(accountId: number, draft: AutoBPSDraft): CreateScheduledTestPlanRequest {
  return { account_id: accountId, model_id: AUTO_BPS_PROBE_MODEL, cron_expression: draft.cronExpression.trim(), enabled: true, max_results: 100,
    auto_recover: false, pelican_config: autoBPSConfig(draft) }
}

export type AutoBPSRuleChange =
  | { kind: 'create'; request: CreateScheduledTestPlanRequest }
  | { kind: 'update'; id: number; request: UpdateScheduledTestPlanRequest }

// 编辑账号保存时要对规则做的改动；弹窗里没动过就返回 null。
// 关掉开关 = 暂停规则（规则、设置和记录都保留，已开着的 BPS 不动），不删除。
export function autoBPSRuleChange(rule: ScheduledTestPlan | null, initial: AutoBPSDraft, draft: AutoBPSDraft, accountId: number): AutoBPSRuleChange | null {
  if (JSON.stringify(initial) === JSON.stringify(draft)) return null
  if (!rule) return draft.enabled ? { kind: 'create', request: autoBPSCreateRequest(accountId, draft) } : null
  if (!draft.enabled) return rule.enabled ? { kind: 'update', id: rule.id, request: { enabled: false } } : null
  const request: UpdateScheduledTestPlanRequest = { enabled: true, pelican_config: autoBPSConfig(draft, rule.pelican_config) }
  // 只改 BPS 选项时不回写计划，保留质量运维中设置的自定义 cron。
  if (draft.cronExpression.trim() !== initial.cronExpression.trim()) request.cron_expression = draft.cronExpression.trim()
  return { kind: 'update', id: rule.id, request }
}
