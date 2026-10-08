import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import RuleList from '../AccountOpsRuleList.vue'
import type { AccountOpsConfig, AccountOpsThresholdAccount } from '@/api/admin/accountOps'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const config: AccountOpsConfig = {
  enabled: false,
  recipient: '',
  balance_low: true,
  weekly_quota: true,
  cooldown_minutes: 60,
  balance_thresholds: [{ account_id: 1, enabled: true, threshold: 5, unit: 'USD' }]
}
const accounts: AccountOpsThresholdAccount[] = [
  { account_id: 1, account_name: 'Alpha key', platform: 'openai', type: 'apikey', balance: 3, unit: 'USD', balance_status: 'ok', received_at: null, usage_windows: [] },
  { account_id: 2, account_name: 'Beta oauth', platform: 'openai', type: 'oauth', balance: null, unit: 'USD', balance_status: 'unsupported', received_at: null, usage_windows: [] },
  { account_id: 3, account_name: 'Gamma key', platform: 'openai', type: 'apikey', balance: 10, unit: 'USD', balance_status: 'ok', received_at: null, usage_windows: [] }
]

function create() {
  return mount(RuleList, {
    props: { accounts, config, loading: false, ready: true, error: '', busy: false }
  })
}

describe('bulk account rule selection', () => {
  it('selects API Key and OAuth accounts together and submits every visible selection', async () => {
    const wrapper = create()
    expect(wrapper.find('[data-testid="batch-edit-rules"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="select-visible-accounts"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="batch-edit-rules"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="select-account-1"]').setValue(true)
    expect(wrapper.get('[data-testid="select-account-2"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid="select-visible-accounts"]').setValue(true)
    await wrapper.get('[data-testid="batch-edit-rules"]').trigger('click')
    expect(wrapper.emitted('batch-edit')).toEqual([[accounts]])
    expect((wrapper.get('[data-testid="select-account-2"]').element as HTMLInputElement).checked).toBe(true)
    await wrapper.get('[data-testid="select-account-1"]').setValue(false)
    expect((wrapper.get('[data-testid="select-visible-accounts"]').element as HTMLInputElement).indeterminate).toBe(true)
    await wrapper.get('[data-testid="batch-edit-rules"]').trigger('click')
    expect(wrapper.emitted('batch-edit')?.[1]).toEqual([[accounts[1], accounts[2]]])
    await wrapper.get('[data-testid="select-visible-accounts"]').setValue(true)
    await wrapper.get('[data-testid="select-visible-accounts"]').setValue(false)
    expect(wrapper.get('[data-testid="batch-edit-rules"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('prunes hidden accounts after search or state filtering and clears selection after a type change', async () => {
    const wrapper = create()
    await wrapper.get('select[aria-label="accountOps.accountType"]').setValue('apikey')
    await wrapper.get('[data-testid="select-visible-accounts"]').setValue(true)
    await wrapper.get('input[aria-label="accountOps.search"]').setValue('Alpha')
    await wrapper.get('[data-testid="batch-edit-rules"]').trigger('click')
    expect(wrapper.emitted('batch-edit')?.[0]).toEqual([[accounts[0]]])

    await wrapper.get('input[aria-label="accountOps.search"]').setValue('')
    expect((wrapper.get('[data-testid="select-account-3"]').element as HTMLInputElement).checked).toBe(false)
    await wrapper.get('select[aria-label="accountOps.ruleState"]').setValue('unconfigured')
    expect(wrapper.get('[data-testid="batch-edit-rules"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="select-visible-accounts"]').setValue(true)
    await wrapper.get('[data-testid="batch-edit-rules"]').trigger('click')
    expect(wrapper.emitted('batch-edit')?.[1]).toEqual([[accounts[2]]])

    await wrapper.get('select[aria-label="accountOps.accountType"]').setValue('all')
    expect(wrapper.get('[data-testid="batch-edit-rules"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('prunes deleted accounts and supports clearing selection after a successful save', async () => {
    const wrapper = create()
    await wrapper.get('[data-testid="select-account-1"]').setValue(true)
    await wrapper.get('[data-testid="select-account-3"]').setValue(true)
    await wrapper.setProps({ accounts: accounts.slice(1) })
    await wrapper.get('[data-testid="batch-edit-rules"]').trigger('click')
    expect(wrapper.emitted('batch-edit')).toEqual([[[accounts[2]]]])
    wrapper.vm.clearSelection()
    await wrapper.vm.$nextTick()
    expect(wrapper.get('[data-testid="batch-edit-rules"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="select-account-2"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('blocks bulk edits while another account operation is saving', async () => {
    const wrapper = create()
    await wrapper.get('[data-testid="select-account-1"]').setValue(true)
    await wrapper.setProps({ busy: true })
    expect(wrapper.get('[data-testid="batch-edit-rules"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="select-visible-accounts"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="select-account-1"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="batch-edit-rules"]').trigger('click')
    expect(wrapper.emitted('batch-edit')).toBeUndefined()
    wrapper.unmount()
  })
})
describe('saved threshold currency', () => {
  it('shows the rule unit separately from a changed sample unit', () => {
    const wrapper = mount(RuleList, { props: { accounts: [{ account_id: 1, account_name: 'key', platform: 'openai', type: 'apikey', balance: 36, unit: 'CNY', balance_status: 'ok', received_at: null, usage_windows: [] }], config: { enabled: false, recipient: '', balance_low: true, weekly_quota: true, cooldown_minutes: 60, balance_thresholds: [{ account_id: 1, enabled: true, threshold: 5, unit: 'USD' }] }, loading: false, ready: true, error: '' } })
    expect(wrapper.text()).toContain('36.00 CNY')
    expect(wrapper.text()).toContain('≤ 5.00 USD')
    expect(wrapper.text()).toContain('accountOps.ruleStates.unknown')
    wrapper.unmount()
  })
})
