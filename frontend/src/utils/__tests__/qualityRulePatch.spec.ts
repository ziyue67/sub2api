import { describe, expect, it } from 'vitest'
import { buildQualityRulePatch, type QualityRuleDraft } from '../qualityRulePatch'
import type { ScheduledTestPlan } from '@/types'

const draft = (): QualityRuleDraft => ({
  model_id: 'new-model', cron_expression: '0 * * * *', enabled: false,
  pelican_config: { question_kind: 'candy', prompt: 'New question', reasoning_effort: 'high', parallel_count: 3,
    quality: { expected_answer: '42', action: 'remove_groups', remove_group_ids: [21], auto_restore: true,
      judge: { group_id: 21, model_id: 'new-judge', prompt: 'Grade the answer' } } },
})
const plan = (): ScheduledTestPlan => ({
  id: 1, account_id: 10, model_id: 'old-model', cron_expression: '*/30 * * * *', enabled: true,
  max_results: 150, auto_recover: false, last_run_at: null, next_run_at: null, created_at: '', updated_at: '',
  pelican_config: { question_kind: 'candy', prompt: 'Own question', reasoning_effort: 'low', parallel_count: 2,
    quality: { expected_answer: '7', action: 'remove_groups', remove_group_ids: [99], auto_restore: false,
      judge: { group_id: 99, model_id: 'own-judge', prompt: 'Own grading instructions' } } },
})

describe('quality rule partial updates', () => {
  it('sends only selected top-level fields, including an explicit false value', () => {
    expect(buildQualityRulePatch(plan(), draft(), ['model', 'schedule', 'enabled'])).toEqual({
      model_id: 'new-model', cron_expression: '0 * * * *', enabled: false,
    })
  })

  it('preserves different questions, judges and target groups when changing restoration', () => {
    const first = plan(), second = plan()
    second.pelican_config!.quality!.remove_group_ids = [100]
    second.pelican_config!.quality!.judge!.model_id = 'another-judge'
    const before = JSON.stringify([first, second])
    for (const rule of [first, second]) {
      expect(buildQualityRulePatch(rule, draft(), ['restore'])).toEqual({
        pelican_config: { ...rule.pelican_config, quality: { ...rule.pelican_config!.quality, auto_restore: true } },
      })
    }
    expect(JSON.stringify([first, second])).toBe(before)
  })

  it('switches to a probe while preserving each rule’s own action and restoration policy', () => {
    const input = draft(); input.pelican_config.question_kind = 'state_probe'
    const updated = buildQualityRulePatch(plan(), input, ['test'])
    expect(updated.pelican_config).toMatchObject({ question_kind: 'state_probe', prompt: '', parallel_count: 1,
      quality: { expected_answer: '', action: 'remove_groups', remove_group_ids: [99], auto_restore: false } })
    expect(updated.pelican_config!.quality).not.toHaveProperty('judge')
  })

  it('does not convert probe rules when editing only failure actions', () => {
    const rule = plan()
    rule.pelican_config!.question_kind = 'state_probe'
    rule.pelican_config!.prompt = ''; rule.pelican_config!.parallel_count = 1
    rule.pelican_config!.quality!.expected_answer = ''; delete rule.pelican_config!.quality!.judge
    expect(buildQualityRulePatch(rule, draft(), ['action']).pelican_config).toEqual({
      ...rule.pelican_config, quality: { ...rule.pelican_config!.quality, remove_group_ids: [21] },
    })
  })

  it('validates selected fields without requiring untouched grading configuration', () => {
    const input = draft(); delete input.pelican_config.quality.judge
    input.pelican_config.quality.remove_group_ids = []
    expect(buildQualityRulePatch(plan(), input, ['enabled'])).toEqual({ enabled: false })
    expect(() => buildQualityRulePatch(plan(), input, ['test'])).toThrow('qualityOps.configureJudge')
    expect(() => buildQualityRulePatch(plan(), input, ['action'])).toThrow('qualityOps.selectGroups')
    expect(() => buildQualityRulePatch(plan(), input, [])).toThrow('qualityOps.selectBulkFields')
  })
})
