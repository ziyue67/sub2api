import { describe, expect, it } from 'vitest'
import { buildQualityRulePatch, qualityBPSError, qualityBPSForm, type QualityRuleDraft } from '../qualityRulePatch'
import type { QualityBPSPolicy, ScheduledTestPlan } from '@/types'

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
const bps = (): QualityBPSPolicy => ({
  failure_threshold: 2, usage_percent: 0, require_all: false, all_models: false, models: ['gpt-6-astra'],
  omit_unsupported_tools: true, ignore_images: false, ignore_encrypted_content: true, auto_disable_on_403: false,
  auto_move_on_403: false, target_group_id: -1, session_proxy: false, proxy_source: 'mihomo', cache_creation_as_input: false,
  pass_threshold: 2, hold_on_usage: true,
})
const probePlan = () => {
  const rule = plan(), config = rule.pelican_config!
  config.question_kind = 'state_probe'; config.prompt = ''; config.parallel_count = 1
  config.quality!.expected_answer = ''; delete config.quality!.judge
  return rule
}

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

  it('copies and normalizes the BPS policy when enabling BPS on probe rules', () => {
    const input = draft(); input.pelican_config.quality.action = 'enable_bps'
    input.pelican_config.quality.bps = { ...bps(), all_models: true, session_proxy: true, proxy_source: '' }
    const updated = buildQualityRulePatch(probePlan(), input, ['action']).pelican_config!.quality!
    expect(updated).toMatchObject({ action: 'enable_bps', remove_group_ids: [] })
    expect(updated.bps).toEqual({ ...bps(), all_models: true, models: [], target_group_id: 0, session_proxy: true, proxy_source: 'mihomo' })
    expect(input.pelican_config.quality.bps!.models).toEqual(['gpt-6-astra'])
  })

  it('applies the BPS turn-off conditions only together with automatic restoration', () => {
    const own = probePlan(), quality = own.pelican_config!.quality!
    quality.action = 'enable_bps'; quality.bps = { ...bps(), usage_percent: 70, pass_threshold: 4, hold_on_usage: false }
    const input = draft(); input.pelican_config.question_kind = 'state_probe'; input.pelican_config.quality.action = 'enable_bps'
    input.pelican_config.quality.bps = { ...bps(), failure_threshold: 3, pass_threshold: 6, hold_on_usage: true }
    // 只改处理方式：各规则保留自己的关闭条件，原本不开 BPS 的规则用默认值。
    expect(buildQualityRulePatch(own, input, ['action']).pelican_config!.quality!.bps).toMatchObject({ failure_threshold: 3, usage_percent: 0, pass_threshold: 4, hold_on_usage: false })
    expect(buildQualityRulePatch(probePlan(), input, ['action']).pelican_config!.quality!.bps).toMatchObject({ failure_threshold: 3, pass_threshold: 2, hold_on_usage: true })
    expect(buildQualityRulePatch(own, input, ['action', 'restore']).pelican_config!.quality!).toMatchObject({ auto_restore: true, bps: { failure_threshold: 3, pass_threshold: 6, hold_on_usage: true } })
    // 只改自动恢复：只动这两项，开启条件和其它选项不变。
    expect(buildQualityRulePatch(own, input, ['restore']).pelican_config!.quality!).toEqual({ ...quality, auto_restore: true, bps: { ...quality.bps, pass_threshold: 6, hold_on_usage: true } })
    // 自动恢复关掉时次数输入框是隐藏的，不拿它覆盖各规则自己的值，也不因它拦截保存。
    input.pelican_config.quality.auto_restore = false; input.pelican_config.quality.bps.pass_threshold = 0
    expect(buildQualityRulePatch(own, input, ['restore']).pelican_config!.quality!).toEqual({ ...quality, auto_restore: false })
    input.pelican_config.quality.auto_restore = true
    expect(() => buildQualityRulePatch(own, input, ['restore'])).toThrow('qualityOps.bpsPassCountInvalid')
    expect(buildQualityRulePatch(plan(), input, ['restore']).pelican_config!.quality!).not.toHaveProperty('bps')
  })

  it('reads the turn-off count back from saved rules', () => {
    expect(qualityBPSForm(undefined)).toMatchObject({ pass_threshold: 2, hold_on_usage: true })
    expect(qualityBPSForm({ ...bps(), pass_threshold: 5, hold_on_usage: false })).toMatchObject({ pass_threshold: 5, hold_on_usage: false })
    expect(qualityBPSForm({ ...bps(), pass_threshold: 0 }).pass_threshold).toBe(1)
  })

  it('drops BPS settings when changing to another action', () => {
    const rule = probePlan(); rule.pelican_config!.quality!.action = 'enable_bps'; rule.pelican_config!.quality!.bps = bps()
    const input = draft(); input.pelican_config.quality.action = 'disable_scheduling'
    const updated = buildQualityRulePatch(rule, input, ['action']).pelican_config!.quality!
    expect(updated).toMatchObject({ action: 'disable_scheduling', remove_group_ids: [] })
    expect(updated).not.toHaveProperty('bps')
  })

  it('never leaves a candy rule with the enable BPS action', () => {
    const input = draft(); input.pelican_config.quality.action = 'enable_bps'; input.pelican_config.quality.bps = bps()
    expect(() => buildQualityRulePatch(plan(), input, ['action'])).toThrow('qualityOps.bpsRequiresProbe')
    const rule = probePlan(); rule.pelican_config!.quality!.action = 'enable_bps'; rule.pelican_config!.quality!.bps = bps()
    expect(() => buildQualityRulePatch(rule, draft(), ['test'])).toThrow('qualityOps.bpsRequiresProbe')
    input.pelican_config.question_kind = 'state_probe'; input.pelican_config.quality.bps = { ...bps(), failure_threshold: 0 }
    expect(() => buildQualityRulePatch(probePlan(), input, ['action'])).toThrow('qualityOps.bpsTriggerRequired')
  })

  it('reports the first invalid BPS setting', () => {
    expect(qualityBPSError(bps())).toBe('')
    expect(qualityBPSError({ ...bps(), failure_threshold: 0, usage_percent: 80 })).toBe('')
    expect(qualityBPSError({ ...bps(), failure_threshold: 1.5 })).toBe('qualityOps.bpsCountInvalid')
    expect(qualityBPSError({ ...bps(), failure_threshold: 101 })).toBe('qualityOps.bpsCountInvalid')
    expect(qualityBPSError({ ...bps(), usage_percent: -1 })).toBe('qualityOps.bpsUsageInvalid')
    expect(qualityBPSError({ ...bps(), usage_percent: Number.NaN })).toBe('qualityOps.bpsUsageInvalid')
    expect(qualityBPSError({ ...bps(), failure_threshold: 0 })).toBe('qualityOps.bpsTriggerRequired')
    expect(qualityBPSError({ ...bps(), models: [' '] })).toBe('qualityOps.bpsModelsRequired')
    expect(qualityBPSError({ ...bps(), models: [], all_models: true })).toBe('')
    expect(qualityBPSError({ ...bps(), auto_move_on_403: true })).toBe('qualityOps.bpsTargetGroupRequired')
    expect(qualityBPSError({ ...bps(), auto_move_on_403: true, target_group_id: 0 })).toBe('')
    expect(qualityBPSError({ ...bps(), pass_threshold: 1 })).toBe('')
    expect(qualityBPSError({ ...bps(), pass_threshold: 100 })).toBe('')
    for (const pass_threshold of [0, 101, 1.5, Number.NaN]) expect(qualityBPSError({ ...bps(), pass_threshold })).toBe('qualityOps.bpsPassCountInvalid')
  })
})
