import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defaultOpenAITwoFAAdmissionConfig, openaiTwoFAAdmissionError } from '../openaiTwoFAAdmission'
import OpenAITwoFAAdmissionCard from '@/components/admin/operations/OpenAITwoFAAdmissionCard.vue'
import AccountAdmissionProgress from '@/components/account/AccountAdmissionProgress.vue'
import type { Account, Group } from '@/types'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('OpenAI 2FA admission', () => {
  it('defaults to the requested policy, disabled until saved and enabled', () => {
    const policy = defaultOpenAITwoFAAdmissionConfig()
    expect(policy).toEqual({ enabled: false, model_id: 'gpt-6.1-sol', reasoning_effort: 'low', wait_seconds: 60, pass_count: 6, passed_concurrency: 2, isolation_count: 6, isolation_group_id: 0 })
    expect(openaiTwoFAAdmissionError(policy)).toBeNull()
    policy.enabled = true
    policy.isolation_group_id = 0
    expect(openaiTwoFAAdmissionError(policy)).toBe('autoConfig.admission.invalid')
  })

  it.each(['wait_seconds', 'pass_count', 'passed_concurrency', 'isolation_count'] as const)('rejects invalid %s', key => {
    for (const value of [0, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(openaiTwoFAAdmissionError({ ...defaultOpenAITwoFAAdmissionConfig(), [key]: value })).toBe('autoConfig.admission.invalid')
    }
  })

  it('only offers active OpenAI groups and keeps an unavailable saved selection visible', () => {
    const groups = [{ id: 44, name: 'Isolation', platform: 'openai', status: 'active' }, { id: 45, name: 'Disabled', platform: 'openai', status: 'disabled' }, { id: 46, name: 'Other', platform: 'anthropic', status: 'active' }] as Group[]
    const w = mount(OpenAITwoFAAdmissionCard, { props: { modelValue: { ...defaultOpenAITwoFAAdmissionConfig(), isolation_group_id: 99 }, groups } })
    const options = w.get('[data-testid="admission-group"]').findAll('option')
    expect(options.map(o => o.attributes('value'))).toEqual(['0', '99', '44'])
    expect(options[1]?.attributes('disabled')).toBeDefined()
    expect(w.get<HTMLInputElement>('[data-testid="admission-model"]').element.value).toBe('gpt-6.1-sol')
    w.unmount()
  })

  it.each(['pending', 'passed', 'isolated', 'isolation_pending'])('shows %s progress independently of ordinary upgrade settings', phase => {
    const account = { extra: { openai_two_fa_admission: { phase, pass_streak: 3, isolation_streak: 1, policy: defaultOpenAITwoFAAdmissionConfig() } } } as unknown as Account
    const w = mount(AccountAdmissionProgress, { props: { account } })
    expect(w.find('[data-testid="admission-progress"]').exists()).toBe(true)
    w.unmount()
  })
})
