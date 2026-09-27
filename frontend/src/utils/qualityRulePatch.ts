import type { PelicanTestConfig, QualityPolicy, ScheduledTestPlan, UpdateScheduledTestPlanRequest } from '@/types'
import { STATE_PROBE_QUESTION } from './intelligenceTest'

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
  }
  if (fields.includes('restore')) config.quality.auto_restore = source.quality.auto_restore
  patch.pelican_config = config
  return patch
}
