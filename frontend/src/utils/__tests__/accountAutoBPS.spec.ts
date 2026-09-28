import { describe, expect, it } from 'vitest'
import type { ScheduledTestPlan } from '@/types'
import {
  AUTO_BPS_CRON,
  AUTO_BPS_PROBE_MODEL,
  autoBPSCreateRequest,
  autoBPSDraftFromRule,
  autoBPSRuleChange,
  newAutoBPSDraft,
  pickAutoBPSRule
} from '../accountAutoBPS'
import { defaultQualityBPS } from '../qualityRulePatch'

function plan(id: number, patch: Partial<ScheduledTestPlan> = {}): ScheduledTestPlan {
  return {
    id, account_id: 9, model_id: 'gpt-6-astra', cron_expression: '*/30 * * * *', enabled: true, max_results: 100,
    auto_recover: false, last_run_at: null, next_run_at: null, created_at: '', updated_at: '',
    pelican_config: { question_kind: 'state_probe', prompt: '', reasoning_effort: 'xhigh', quality: {
      expected_answer: '', action: 'enable_bps', remove_group_ids: [], auto_restore: false,
      bps: { ...defaultQualityBPS(), failure_threshold: 3, all_models: true, models: [] } } },
    ...patch
  }
}

describe('accountAutoBPS', () => {
  it('picks the running auto-BPS rule, otherwise the oldest one', () => {
    const otherAction = plan(1, { pelican_config: { question_kind: 'state_probe', quality: { expected_answer: '', action: 'remove_groups', remove_group_ids: [2], auto_restore: false } } })
    const pelican = plan(2, { pelican_config: { question_kind: 'pelican', quality: { expected_answer: 'x', action: 'enable_bps', remove_group_ids: [], auto_restore: false } } })
    const noConfig = plan(3, { pelican_config: undefined })
    expect(pickAutoBPSRule([otherAction, pelican, noConfig])).toBeNull()
    expect(pickAutoBPSRule([plan(8, { enabled: false }), plan(6, { enabled: false })])?.id).toBe(6)
    expect(pickAutoBPSRule([plan(4, { enabled: false }), otherAction, plan(7), plan(5)])?.id).toBe(5)
  })

  it('reads the switch and settings back from a rule', () => {
    expect(autoBPSDraftFromRule(null)).toEqual(newAutoBPSDraft())
    expect(newAutoBPSDraft()).toMatchObject({ enabled: false, autoRestore: true })
    const draft = autoBPSDraftFromRule(plan(1, { enabled: false }))
    expect(draft.enabled).toBe(false)
    expect(draft.autoRestore).toBe(false)
    expect(draft.bps.failure_threshold).toBe(3)
    expect(draft.bps.all_models).toBe(true)
    expect(draft.bps.models.length).toBeGreaterThan(0)
  })

  it('creates a state-probe rule that enables BPS', () => {
    const draft = { ...newAutoBPSDraft(), enabled: true }
    draft.bps.all_models = true
    expect(autoBPSCreateRequest(42, draft)).toEqual({
      account_id: 42, model_id: AUTO_BPS_PROBE_MODEL, cron_expression: AUTO_BPS_CRON, enabled: true, max_results: 100, auto_recover: false,
      pelican_config: { reasoning_effort: 'high', question_kind: 'state_probe', prompt: '', parallel_count: 1, quality: {
        expected_answer: '', action: 'enable_bps', remove_group_ids: [], auto_restore: true,
        bps: { ...draft.bps, models: [], target_group_id: 0, proxy_source: '' } } }
    })
  })

  it('only touches the rule when the switch or settings changed', () => {
    const off = newAutoBPSDraft()
    const on = { ...newAutoBPSDraft(), enabled: true }
    expect(autoBPSRuleChange(null, off, off, 9)).toBeNull()
    expect(autoBPSRuleChange(null, off, { ...off, autoRestore: false }, 9)).toBeNull()
    expect(autoBPSRuleChange(null, off, on, 9)).toEqual({ kind: 'create', request: autoBPSCreateRequest(9, on) })

    const running = plan(5)
    const loaded = autoBPSDraftFromRule(running)
    expect(autoBPSRuleChange(running, loaded, loaded, 9)).toBeNull()
    expect(autoBPSRuleChange(running, loaded, { ...loaded, enabled: false }, 9)).toEqual({ kind: 'update', id: 5, request: { enabled: false } })

    const paused = plan(6, { enabled: false })
    const pausedDraft = autoBPSDraftFromRule(paused)
    expect(autoBPSRuleChange(paused, pausedDraft, { ...pausedDraft, autoRestore: true }, 9)).toBeNull()
  })

  it('updates settings in place and keeps the rule’s own probe options', () => {
    const running = plan(5)
    const loaded = autoBPSDraftFromRule(running)
    const edited = { ...loaded, autoRestore: true, bps: { ...loaded.bps, failure_threshold: 1 } }
    const change = autoBPSRuleChange(running, loaded, edited, 9)
    expect(change?.kind).toBe('update')
    if (change?.kind !== 'update') return
    expect(change.id).toBe(5)
    expect(change.request.enabled).toBe(true)
    expect(change.request.pelican_config?.reasoning_effort).toBe('xhigh')
    expect(change.request.pelican_config?.quality).toMatchObject({ action: 'enable_bps', auto_restore: true, bps: { failure_threshold: 1, models: [] } })

    const paused = plan(6, { enabled: false })
    const pausedDraft = autoBPSDraftFromRule(paused)
    const resumed = autoBPSRuleChange(paused, pausedDraft, { ...pausedDraft, enabled: true }, 9)
    expect(resumed).toMatchObject({ kind: 'update', id: 6, request: { enabled: true } })
  })
})
