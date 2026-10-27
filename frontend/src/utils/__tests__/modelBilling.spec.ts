import { describe, expect, it } from 'vitest'
import { defaultModelBillingConfig, modelBillingConfigError, modelBillingPayload } from '../modelBilling'

describe('model billing rules', () => {
  it.each(['', '*', 'lu*na', 'luna?', 'luna x', 'luna[1]', 'a'.repeat(201)])('rejects invalid model patterns: %s', model => {
    expect(modelBillingConfigError({ enabled: true, rules: [{ model, multiplier: 10 }] })).not.toBeNull()
  })
  it.each(['gpt-6-luna', 'gpt-6-luna*', 'vendor/luna*', 'gpt-6-luna:high', ' GPT-6-LUNA* '])('accepts exact names and prefix rules: %s', model => {
    expect(modelBillingConfigError({ enabled: true, rules: [{ model, multiplier: 1.25 }] })).toBeNull()
  })
  it('returns independent defaults', () => {
    const first = defaultModelBillingConfig()
    first.rules[0].multiplier = 2
    expect(defaultModelBillingConfig().rules[0].multiplier).toBe(10)
  })
})

describe('per-rule model billing groups', () => {
  it.each([undefined, null, [], [5], [5, 6, 5]])('accepts legacy/all and positive scopes: %j', group_ids => {
    expect(modelBillingConfigError({ enabled: true, rules: [{ model: 'luna', multiplier: 10, group_ids }] })).toBeNull()
  })
  it.each([[0], [-1], [1.5], [NaN], [Infinity], [Number.MAX_SAFE_INTEGER + 1], ['5']])('rejects malformed IDs: %j', group_ids => {
    expect(modelBillingConfigError({ enabled: false, rules: [{ model: 'luna', multiplier: 10, group_ids: group_ids as number[] }] })).not.toBeNull()
  })
})

it('emits explicit all-groups arrays and deduplicates IDs without changing the draft', () => {
 const cfg = { enabled: true, rules: [{ model: ' luna ', multiplier: 10, group_ids: [5,6,5] }, { model: 'other', multiplier: 2 }] }
 expect(modelBillingPayload(cfg).rules).toEqual([{model:'luna',multiplier:10,group_ids:[5,6]},{model:'other',multiplier:2,group_ids:[]}])
 expect(cfg.rules[0].group_ids).toEqual([5,6,5])
})
