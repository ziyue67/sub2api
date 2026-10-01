import { describe, expect, it } from 'vitest'
import { defaultModelBillingConfig, modelBillingConfigError } from '../modelBilling'

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
