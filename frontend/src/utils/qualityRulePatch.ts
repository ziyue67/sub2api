import type { PelicanTestConfig, QualityBPSPolicy, QualityPolicy, ScheduledTestPlan, UpdateScheduledTestPlanRequest } from '@/types'
import { DEFAULT_EXCEL_BPS_MODELS } from '@/constants/account'
import { STATE_PROBE_QUESTION } from './intelligenceTest'
import { DEFAULT_BPS_RECOVERY_INTERVAL_MINUTES, isValidBPSRecoveryInterval } from './excelBPSRecovery'

// 新建「降智开 BPS」时的默认勾选；target_group_id = -1 表示还没选 403 后的目标分组。
export function defaultQualityBPS(): QualityBPSPolicy {
  return { failure_threshold: 2, usage_percent: 0, require_all: false, all_models: false, models: [...DEFAULT_EXCEL_BPS_MODELS],
    omit_unsupported_tools: false, ignore_images: false, ignore_encrypted_content: true, auto_disable_on_403: true, auto_recover_on_403: false, recovery_interval_minutes: DEFAULT_BPS_RECOVERY_INTERVAL_MINUTES, auto_move_on_403: false,
    target_group_id: -1, session_proxy: false, proxy_source: 'mihomo', cache_creation_as_input: true, pass_threshold: 2, hold_on_usage: true }
}

// 已保存的 BPS 设置盖在默认值上；「全部模型」时后端不存模型列表，切回按模型时给默认候选。
// 早先保存、没有满血关闭次数的规则后端按 1 次处理，表单也显示 1。
export function qualityBPSForm(saved?: Partial<QualityBPSPolicy> | null): QualityBPSPolicy {
  const value = saved || {}
  return { ...defaultQualityBPS(), ...value,
    models: value.models?.length ? [...value.models] : [...DEFAULT_EXCEL_BPS_MODELS],
    target_group_id: value.auto_move_on_403 ? value.target_group_id ?? -1 : -1,
    proxy_source: value.proxy_source || 'mihomo',
    pass_threshold: saved ? saved.pass_threshold || 1 : defaultQualityBPS().pass_threshold }
}

const validPassCount = (count: number) => Number.isInteger(count) && count >= 1 && count <= 100

// 与后端 validateQualityBPSPolicy 对应的前置检查，返回 i18n 键；空串表示通过。
export function qualityBPSError(bps: QualityBPSPolicy): string {
  const count = bps.failure_threshold, usage = bps.usage_percent
  if (!Number.isInteger(count) || count < 0 || count > 100) return 'qualityOps.bpsCountInvalid'
  if (!Number.isFinite(usage) || usage < 0 || usage > 100) return 'qualityOps.bpsUsageInvalid'
  if (!count && !usage) return 'qualityOps.bpsTriggerRequired'
  if (!validPassCount(bps.pass_threshold)) return 'qualityOps.bpsPassCountInvalid'
  if (!bps.all_models && !bps.models.some(model => model.trim())) return 'qualityOps.bpsModelsRequired'
  if (bps.recovery_interval_minutes !== undefined && !isValidBPSRecoveryInterval(bps.recovery_interval_minutes)) return 'admin.accounts.openai.excelBPS403RecoveryIntervalInvalid'
  if (bps.auto_move_on_403 && !(bps.target_group_id >= 0)) return 'qualityOps.bpsTargetGroupRequired'
  return ''
}

// 表单里「未选目标分组」用 -1 占位；提交前按后端口径归一，避免未勾选的子项残留旧值。
export function qualityBPSPayload(bps: QualityBPSPolicy): QualityBPSPolicy {
  return {
    ...bps,
    recovery_interval_minutes: bps.recovery_interval_minutes ?? DEFAULT_BPS_RECOVERY_INTERVAL_MINUTES,
    models: bps.all_models ? [] : [...bps.models],
    target_group_id: bps.auto_move_on_403 ? bps.target_group_id : 0,
    proxy_source: bps.session_proxy ? bps.proxy_source || 'mihomo' : '',
  }
}

export const qualityRuleFields = ['model', 'schedule', 'test', 'action', 'restore', 'enabled'] as const
export type QualityRuleField = typeof qualityRuleFields[number]
export type QualityRuleDraft = {
  model_id: string
  cron_expression: string
  enabled: boolean
  pelican_config: PelicanTestConfig & { quality: QualityPolicy }
}

// The API replaces pelican_config as a whole. Merge selected fields into each
// rule's own configuration, never into a shared copy of the first rule.
export function buildQualityRulePatch(
  plan: ScheduledTestPlan,
  draft: QualityRuleDraft,
  fields: readonly QualityRuleField[],
): UpdateScheduledTestPlanRequest {
  const patch: UpdateScheduledTestPlanRequest = {}
  if (!fields.length) throw new Error('qualityOps.selectBulkFields')
  if (fields.includes('model')) {
    if (!draft.model_id.trim()) throw new Error('qualityOps.modelRequired')
    patch.model_id = draft.model_id.trim()
  }
  if (fields.includes('schedule')) {
    if (!draft.cron_expression.trim()) throw new Error('qualityOps.scheduleRequired')
    patch.cron_expression = draft.cron_expression.trim()
  }
  if (fields.includes('enabled')) patch.enabled = draft.enabled
  if (!fields.some(field => ['test', 'action', 'restore'].includes(field))) return patch

  if (!plan.pelican_config?.quality) throw new Error('qualityOps.ruleConfigMissing')
  const config: PelicanTestConfig & { quality: QualityPolicy } = JSON.parse(JSON.stringify(plan.pelican_config))
  const source = draft.pelican_config
  if (fields.includes('test')) {
    config.question_kind = source.question_kind
    if (source.test_channel) config.test_channel = source.test_channel
    else delete config.test_channel
    config.reasoning_effort = source.reasoning_effort
    const probe = source.question_kind === STATE_PROBE_QUESTION
    config.prompt = probe ? '' : source.prompt
    config.parallel_count = probe ? 1 : source.parallel_count
    config.quality.expected_answer = probe ? '' : source.quality.expected_answer
    if (probe) delete config.quality.judge
    else {
      const judge = source.quality.judge
      if (!judge?.group_id || !judge.model_id.trim() || !judge.prompt.trim()) throw new Error('qualityOps.configureJudge')
      if (!config.prompt.trim() || !config.quality.expected_answer.trim()) throw new Error('qualityOps.questionRequired')
      config.quality.judge = { ...judge }
    }
  }
  if (fields.includes('action')) {
    config.quality.action = source.quality.action
    config.quality.remove_group_ids = source.quality.action === 'remove_groups' ? [...source.quality.remove_group_ids] : []
    if (config.quality.action === 'remove_groups' && !config.quality.remove_group_ids.length) throw new Error('qualityOps.selectGroups')
    if (config.quality.action === 'enable_bps') {
      if (!source.quality.bps) throw new Error('qualityOps.bpsTriggerRequired')
      const error = qualityBPSError(source.quality.bps)
      if (error) throw new Error(error)
      config.quality.bps = qualityBPSPayload(source.quality.bps)
    } else delete config.quality.bps
  }
  // Explicit BPS observation includes giving up this rule's account actions.
  if (config.test_channel === 'bps') config.quality.action = 'observe_only'
  if (config.quality.action === 'observe_only') {
    config.quality.remove_group_ids = []
    config.quality.auto_restore = false
    delete config.quality.bps
  }
  // Automatic BPS switching remains a separate native-probe policy.
  if (config.quality.action === 'enable_bps' && config.question_kind !== STATE_PROBE_QUESTION) throw new Error('qualityOps.bpsRequiresProbe')
  if (fields.includes('restore') && config.quality.action !== 'observe_only') config.quality.auto_restore = source.quality.auto_restore
  // 满血关闭次数和「用量仍高时先不关」跟着「自动恢复」走：勾选修改自动恢复且开着时才用表单里的值，
  // 否则各规则保留自己的；原本不是开 BPS 的规则用默认值。
  if (config.quality.action === 'enable_bps' && config.quality.bps) {
    const draftBPS = fields.includes('restore') && source.quality.auto_restore ? source.quality.bps : undefined
    if (draftBPS && !validPassCount(draftBPS.pass_threshold)) throw new Error('qualityOps.bpsPassCountInvalid')
    const from = draftBPS || plan.pelican_config.quality.bps || defaultQualityBPS()
    config.quality.bps.pass_threshold = from.pass_threshold
    config.quality.bps.hold_on_usage = from.hold_on_usage
  }
  patch.pelican_config = config
  return patch
}
