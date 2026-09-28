import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import AccountQualityView from '../AccountQualityView.vue'
import * as accountsAPI from '@/api/admin/accounts'
import scheduledTests from '@/api/admin/scheduledTests'
import { listQualityPlans, listQualityOperations } from '@/api/admin/accountQuality'
import { useAccountQualityStore } from '@/stores/accountQuality'
import type { ScheduledTestPlan } from '@/types'
vi.mock('@/components/admin/operations/SmartOpsNav.vue', () => ({ default: { template: '<nav />' } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1, role: 'admin' } }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key, te: () => true }) }))
vi.mock('@/api/admin/accountQuality', () => ({ listQualityPlans: vi.fn(), runQualityPlan: vi.fn(), listQualityOperations: vi.fn().mockResolvedValue({items:[],next_cursor:0}) }))
vi.mock('@/api/admin/scheduledTests', () => ({ default: { create: vi.fn(), update: vi.fn(), delete: vi.fn(), listResults: vi.fn(), getResult: vi.fn() } }))
vi.mock('@/api/admin/accounts', () => ({ list: vi.fn().mockResolvedValue({ items: [{ id: 1, name: 'Test account', platform: 'openai', type: 'oauth' }], total: 1 }) }))
vi.mock('@/api/admin/groups', () => ({ getModelAllowlistCandidates: vi.fn().mockResolvedValue(["test-judge"]), getAllIncludingInactive: vi.fn().mockResolvedValue([{ id: 21, name: 'Quality pool', status:'active', platform: 'openai' }]) }))
vi.mock('@/components/account/ModelWhitelistSelector.vue', () => ({ default: { props: ['modelValue'], template: '<div data-testid="model-selector">{{ modelValue.join(",") }}</div>' } }))
const mountView = () => mount(AccountQualityView, { global: { plugins: [createPinia()], stubs: { Teleport: true, AppLayout: { template: '<main><slot /></main>' } } } })
describe('quality operations', () => {
  beforeEach(() => { vi.clearAllMocks(); vi.mocked(scheduledTests.update).mockReset(); vi.mocked(accountsAPI.list).mockReset().mockResolvedValue({ items: [{ id: 1, name: 'Test account', platform: 'openai', type: 'oauth' }], total: 1 } as any); vi.mocked(listQualityPlans).mockResolvedValue([]); vi.mocked(listQualityOperations).mockResolvedValue({items:[],next_cursor:0}) })
  const rules = (): ScheduledTestPlan[] => [1, 2, 3].map(id => ({
    id, account_id: id, account_name: `Account ${id}`, model_id: `model-${id}`, cron_expression: '*/30 * * * *', enabled: true,
    max_results: 100, auto_recover: false, last_run_at: null, next_run_at: null, created_at: '', updated_at: '',
    pelican_config: { question_kind: 'state_probe', prompt: '', reasoning_effort: 'high', parallel_count: 1,
      quality: { expected_answer: '', action: 'remove_groups', remove_group_ids: [id], auto_restore: false } },
  }))

  it('selects search matches independently of the history filter and preserves hidden selections', async () => {
    vi.mocked(listQualityPlans).mockResolvedValue(rules())
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-plan-id="1"] .rule-checkbox input').setValue(true)
    await wrapper.get('[data-plan-id="2"] .rule-select').trigger('click')
    expect(useAccountQualityStore().selectedPlanId).toBe(2)
    await wrapper.get('.rule-search input').setValue('Account 2')
    await wrapper.get('[data-testid="quality-select-rules"]').setValue(true)
    await wrapper.get('.rule-search input').setValue('')
    expect(wrapper.get('[data-testid="quality-select-rules"]').element).toHaveProperty('indeterminate', true)
    expect(wrapper.get('[data-plan-id="1"] .rule-checkbox input').element).toHaveProperty('checked', true)
    expect(wrapper.get('[data-plan-id="2"] .rule-checkbox input').element).toHaveProperty('checked', true)
    expect(wrapper.get('[data-plan-id="3"] .rule-checkbox input').element).toHaveProperty('checked', false)
    await wrapper.get('[data-testid="quality-clear-rules"]').trigger('click')
    expect(wrapper.get('[data-testid="quality-bulk-edit"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('opens bulk editing without preselection and selects only matching accounts with rules across pages', async () => {
    vi.mocked(listQualityPlans).mockResolvedValue(rules())
    vi.mocked(accountsAPI.list).mockResolvedValue({ items: [{ id: 2, name: 'Account 2' }, { id: 4, name: 'No rule' }], total: 51 } as any)
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="quality-bulk-edit"]').trigger('click'); await flushPromises()
    expect(wrapper.get('button[form="quality-rule-form"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('#quality-rule-form input[type="checkbox"][value="4"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="quality-account-group"]').setValue('21')
    await wrapper.get('[data-testid="quality-account-type"]').setValue('apikey')
    await wrapper.get('#quality-account-search').setValue('matching')
    await wrapper.get('#quality-account-search').trigger('keydown', { key: 'Enter' }); await flushPromises()
    vi.mocked(accountsAPI.list).mockClear()
      .mockResolvedValueOnce({ items: [{ id: 2 }, { id: 4 }], total: 51 } as any)
      .mockResolvedValueOnce({ items: [{ id: 3 }, { id: 2 }], total: 51 } as any)
    await wrapper.get('[data-testid="quality-select-all"]').trigger('click'); await flushPromises()
    expect(accountsAPI.list).toHaveBeenLastCalledWith(2, 50, expect.objectContaining({ group: '21', type: 'apikey', search: 'matching' }))
    expect((wrapper.vm as any).bulkRuleIds).toEqual([2, 3])
    await wrapper.get('[data-testid="quality-account-type"]').setValue('oauth'); await flushPromises()
    expect((wrapper.vm as any).bulkRuleIds).toEqual([2, 3])
    await wrapper.get('[data-testid="quality-bulk-field-model"]').setValue(true)
    await wrapper.get('input[placeholder="gpt-6-astra"]').setValue('new-model')
    await wrapper.get('#quality-rule-form').trigger('submit'); await flushPromises()
    expect(vi.mocked(scheduledTests.update).mock.calls).toEqual([[2, { model_id: 'new-model' }], [3, { model_id: 'new-model' }]])
    expect(scheduledTests.create).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('removes unsupported selections when switching to probes and excludes them from select all', async () => {
    const accounts = [
      { id: 1, name: 'OAuth', platform: 'openai', type: 'oauth' },
      { id: 2, name: 'Key', platform: 'openai', type: 'apikey' },
      { id: 3, name: 'Claude', platform: 'anthropic', type: 'oauth' },
      { id: 4, name: 'Setup', platform: 'openai', type: 'setup-token' },
    ]
    vi.mocked(accountsAPI.list).mockResolvedValue({ items: accounts, total: 4 } as any)
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises()
    await wrapper.get('[data-testid="quality-select-page"]').trigger('click')
    expect(vm.selectedAccounts).toEqual([1, 2, 3, 4])
    await wrapper.get('[data-testid="quality-question-kind"]').setValue('state_probe')
    expect(vm.selectedAccounts).toEqual([1, 4])
    expect(wrapper.get('#quality-rule-form input[type="checkbox"][value="2"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="quality-select-all"]').trigger('click'); await flushPromises()
    expect(vm.selectedAccounts).toEqual([1, 4])
    vm.form.pelican_config.quality.action = 'disable_scheduling'
    await vm.save()
    expect(vi.mocked(scheduledTests.create).mock.calls.map(([request]) => request.account_id)).toEqual([1, 4])
    wrapper.unmount()
  })

  it('prunes deleted selections after a refresh', async () => {
    vi.mocked(listQualityPlans).mockResolvedValue(rules())
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="quality-select-rules"]').setValue(true)
    vi.mocked(listQualityPlans).mockResolvedValue([])
    await useAccountQualityStore().refreshRules(true); await flushPromises()
    expect(wrapper.get('[data-testid="quality-bulk-edit"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('applies only checked fields, continues after a failure and retries only failed rules', async () => {
    vi.mocked(listQualityPlans).mockResolvedValue(rules())
    vi.mocked(scheduledTests.update).mockResolvedValueOnce(rules()[0]).mockRejectedValueOnce(new Error('Temporary failure')).mockResolvedValue(rules()[2])
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="quality-select-rules"]').setValue(true)
    await wrapper.get('[data-testid="quality-bulk-edit"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-testid="quality-account-group"]').exists()).toBe(true)
    expect(wrapper.get('button[form="quality-rule-form"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="quality-bulk-field-model"]').setValue(true)
    await wrapper.get('input[placeholder="gpt-6-astra"]').setValue('new-model')
    await wrapper.get('#quality-rule-form').trigger('submit'); await flushPromises()
    expect(vi.mocked(scheduledTests.update).mock.calls).toEqual([[1, { model_id: 'new-model' }], [2, { model_id: 'new-model' }], [3, { model_id: 'new-model' }]])
    expect(wrapper.text()).toContain('Temporary failure')
    expect(wrapper.find('#quality-rule-form').exists()).toBe(true)
    await wrapper.get('#quality-rule-form').trigger('submit'); await flushPromises()
    expect(vi.mocked(scheduledTests.update).mock.calls.map(([id]) => id)).toEqual([1, 2, 3, 2])
    expect(wrapper.find('#quality-rule-form').exists()).toBe(false)
    wrapper.unmount()
  })

  it('merges nested changes into refreshed per-rule settings', async () => {
    vi.mocked(listQualityPlans).mockResolvedValue(rules())
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="quality-select-rules"]').setValue(true)
    await wrapper.get('[data-testid="quality-bulk-edit"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="quality-bulk-field-restore"]').setValue(true)
    const vm = wrapper.vm as any
    vm.form.pelican_config.quality.auto_restore = true
    const refreshed = rules(); refreshed[1].pelican_config!.quality!.remove_group_ids = [99]
    vi.mocked(listQualityPlans).mockResolvedValue(refreshed)
    await wrapper.get('#quality-rule-form').trigger('submit'); await flushPromises()
    const bodies = vi.mocked(scheduledTests.update).mock.calls.map(([, body]) => body)
    expect(bodies.map(body => body.pelican_config!.quality!.remove_group_ids)).toEqual([[1], [99], [3]])
    expect(bodies.every(body => body.pelican_config!.quality!.auto_restore)).toBe(true)
    expect(bodies.every(body => !('model_id' in body) && !('enabled' in body))).toBe(true)
    wrapper.unmount()
  })

  it('stops before any write if a selected rule disappeared or refreshing fails', async () => {
    vi.mocked(listQualityPlans).mockResolvedValue(rules())
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="quality-select-rules"]').setValue(true)
    await wrapper.get('[data-testid="quality-bulk-edit"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="quality-bulk-field-enabled"]').setValue(true)
    vi.mocked(listQualityPlans).mockRejectedValueOnce(new Error('Refresh failed'))
    await wrapper.get('#quality-rule-form').trigger('submit'); await flushPromises()
    expect(wrapper.text()).toContain('Refresh failed')
    expect(scheduledTests.update).not.toHaveBeenCalled()
    vi.mocked(listQualityPlans).mockResolvedValue(rules().slice(0, 2))
    await wrapper.get('#quality-rule-form').trigger('submit'); await flushPromises()
    expect(wrapper.text()).toContain('qualityOps.ruleUnavailable')
    expect(scheduledTests.update).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('blocks duplicate saves and stops a running batch on unmount', async () => {
    vi.mocked(listQualityPlans).mockResolvedValue(rules())
    let resolve!: (value: ScheduledTestPlan) => void
    vi.mocked(scheduledTests.update).mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="quality-select-rules"]').setValue(true)
    await wrapper.get('[data-testid="quality-bulk-edit"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="quality-bulk-field-enabled"]').setValue(true)
    await wrapper.get('#quality-rule-form').trigger('submit'); await flushPromises()
    await wrapper.get('#quality-rule-form').trigger('submit'); await flushPromises()
    expect(scheduledTests.update).toHaveBeenCalledTimes(1)
    wrapper.unmount(); resolve(rules()[0]); await flushPromises()
    expect(scheduledTests.update).toHaveBeenCalledTimes(1)
  })
  it.each(['oauth', 'apikey'])('combines %s type, group and search across pages', async (type) => {
    vi.mocked(accountsAPI.list).mockResolvedValue({ items: [{ id: 2, name: 'Matching account' }], total: 51 } as any)
    const wrapper = mountView(); await flushPromises()
    const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises()
    await wrapper.get('[data-testid="quality-account-type"]').setValue(type)
    await wrapper.get('[data-testid="quality-account-group"]').setValue('21')
    await wrapper.get('#quality-account-search').setValue('  matching  ')
    await wrapper.get('#quality-account-search').trigger('keydown', { key: 'Enter' }); await flushPromises()
    expect(accountsAPI.list).toHaveBeenLastCalledWith(1, 50, expect.objectContaining({ type, group: '21', search: 'matching', lite: 'true' }))
    await wrapper.get('button[aria-label="qualityOps.nextAccountPage"]').trigger('click'); await flushPromises()
    expect(accountsAPI.list).toHaveBeenLastCalledWith(2, 50, expect.objectContaining({ type, group: '21', search: 'matching' }))
    await wrapper.get('[data-testid="quality-account-type"]').setValue(''); await flushPromises()
    expect(accountsAPI.list).toHaveBeenLastCalledWith(1, 50, expect.objectContaining({ type: undefined, group: '21', search: 'matching' }))
    wrapper.unmount()
  })
  it('selects only eligible accounts on this page and keeps selections across filters', async () => {
    vi.mocked(listQualityPlans).mockResolvedValue([{ id: 4, account_id: 1 }] as any)
    vi.mocked(accountsAPI.list).mockResolvedValue({ items: [{ id: 1, name: 'Rule exists' }, { id: 2, name: 'New account' }], total: 51 } as any)
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises(); vm.selectedAccounts = [99]
    await wrapper.get('[data-testid="quality-select-page"]').trigger('click')
    await wrapper.get('[data-testid="quality-select-page"]').trigger('click')
    expect(vm.selectedAccounts).toEqual([99, 2])
    expect(wrapper.get('input[type="checkbox"][value="1"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="quality-account-group"]').setValue('ungrouped'); await flushPromises()
    expect(accountsAPI.list).toHaveBeenLastCalledWith(1, 50, expect.objectContaining({ group: 'ungrouped' }))
    expect(vm.selectedAccounts).toEqual([99, 2])
    await wrapper.get('[data-testid="quality-clear-selection"]').trigger('click')
    expect(vm.selectedAccounts).toEqual([])
    wrapper.unmount()
  })
  it('selects all filtered pages without duplicate IDs or accounts with rules', async () => {
    vi.mocked(listQualityPlans).mockResolvedValue([{ id: 4, account_id: 1 }] as any)
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises()
    await wrapper.get('[data-testid="quality-account-type"]').setValue('oauth')
    await wrapper.get('[data-testid="quality-account-group"]').setValue('21'); await flushPromises()
    await wrapper.get('#quality-account-search').setValue('demo')
    await wrapper.get('#quality-account-search').trigger('keydown', { key: 'Enter' }); await flushPromises()
    vm.selectedAccounts = [99, 2]
    vi.mocked(accountsAPI.list).mockClear()
      .mockResolvedValueOnce({ items: [{ id: 1 }, { id: 2 }], total: 51 } as any)
      .mockResolvedValueOnce({ items: [{ id: 2 }, { id: 3 }], total: 51 } as any)
    await wrapper.get('[data-testid="quality-select-all"]').trigger('click'); await flushPromises()
    expect(accountsAPI.list).toHaveBeenNthCalledWith(1, 1, 50, expect.objectContaining({ type: 'oauth', group: '21', search: 'demo', sort_by: 'id', sort_order: 'asc' }))
    expect(accountsAPI.list).toHaveBeenNthCalledWith(2, 2, 50, expect.objectContaining({ type: 'oauth', group: '21', search: 'demo' }))
    expect(vm.selectedAccounts).toEqual([99, 2, 3])
    vm.form.pelican_config.quality.judge = { group_id: 21, model_id: 'test-judge', prompt: 'grade' }
    vm.form.pelican_config.quality.action = 'disable_scheduling'
    await vm.save()
    expect(vi.mocked(scheduledTests.create).mock.calls.map(([request]) => request.account_id)).toEqual([99, 2, 3])
    wrapper.unmount()
  })
  it('keeps the original selection if a later bulk page fails and allows retry', async () => {
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises(); vm.selectedAccounts = [99]
    vi.mocked(accountsAPI.list).mockResolvedValueOnce({ items: [{ id: 2 }], total: 51 } as any).mockRejectedValueOnce(new Error('Page failed'))
    await wrapper.get('[data-testid="quality-select-all"]').trigger('click'); await flushPromises()
    expect(vm.selectedAccounts).toEqual([99]); expect(wrapper.text()).toContain('Page failed')
    await wrapper.get('[data-testid="quality-select-all"]').trigger('click'); await flushPromises()
    expect(vm.selectedAccounts).toEqual([99, 1]); expect(wrapper.text()).not.toContain('Page failed')
    wrapper.unmount()
  })
  it.each(['filter', 'clear', 'close'])('discards an in-flight bulk selection after %s', async (action) => {
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises()
    let resolve!: (value: any) => void
    vi.mocked(accountsAPI.list).mockImplementationOnce(() => new Promise(done => { resolve = done }))
    await wrapper.get('[data-testid="quality-select-all"]').trigger('click')
    expect(wrapper.get('button[form="quality-rule-form"]').attributes('disabled')).toBeDefined()
    if (action === 'filter') await wrapper.get('[data-testid="quality-account-type"]').setValue('apikey')
    else if (action === 'clear') await wrapper.get('[data-testid="quality-clear-selection"]').trigger('click')
    else { vm.closeForm(); vm.newPlan() }
    resolve({ items: [{ id: 77 }], total: 51 }); await flushPromises()
    expect(vm.selectedAccounts).toEqual([])
    expect(vm.selectingAccounts).toBe(false)
    expect(accountsAPI.list).not.toHaveBeenCalledWith(2, 50, expect.anything())
    wrapper.unmount()
  })
  it('ignores old list responses after filters change and clears failed results', async () => {
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises()
    let resolve!: (value: any) => void
    vi.mocked(accountsAPI.list).mockImplementationOnce(() => new Promise(done => { resolve = done }))
    await wrapper.get('[data-testid="quality-account-type"]').setValue('oauth')
    vi.mocked(accountsAPI.list).mockResolvedValueOnce({ items: [{ id: 2, name: 'API result' }], total: 1 } as any)
    await wrapper.get('[data-testid="quality-account-type"]').setValue('apikey'); await flushPromises()
    resolve({ items: [{ id: 77, name: 'Stale OAuth result' }], total: 1 }); await flushPromises()
    expect(vm.accounts.map((account: any) => account.id)).toEqual([2])
    vi.mocked(accountsAPI.list).mockRejectedValueOnce(new Error('Search failed'))
    await wrapper.get('[data-testid="quality-account-group"]').setValue('21'); await flushPromises()
    expect(vm.accounts).toEqual([])
    expect(wrapper.text()).toContain('Search failed')
    expect(wrapper.get('[data-testid="quality-select-all"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
  it('prefills the test model and submits it for a new rule', async () => {
    const wrapper = mountView(); await flushPromises()
    const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises()
    expect(wrapper.find('input[placeholder="gpt-6-astra"]').element).toHaveProperty('value', 'gpt-6-astra')
    vm.selectedAccounts = [1]
    vm.form.pelican_config.quality.judge = { group_id: 21, model_id: 'test-judge', prompt: 'grade semantically' }
    vm.form.pelican_config.quality.remove_group_ids = [21]
    await vm.save()
    expect(scheduledTests.create).toHaveBeenCalledWith(expect.objectContaining({ account_id: 1, model_id: 'gpt-6-astra' }))
    wrapper.unmount()
  })
  it('requires explicit group selection and keeps automatic restoration opt-in', async () => {
    const wrapper = mountView(); await flushPromises()
    const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises(); vm.selectedAccounts = [1]; vm.form.pelican_config.quality.judge = {group_id:21,model_id:'test-judge',prompt:'grade semantically'}; vm.form.model_id = 'test-model'
    await vm.save()
    expect(scheduledTests.create).not.toHaveBeenCalled()
    expect(wrapper.find('[role="alert"]').text()).toContain('qualityOps.selectGroups')
    vm.form.pelican_config.quality.remove_group_ids = [21]
    await vm.save()
    expect(scheduledTests.create).toHaveBeenCalledWith(expect.objectContaining({ account_id: 1, auto_recover: false, pelican_config: expect.objectContaining({ quality: { expected_answer: '21', action: 'remove_groups', remove_group_ids: [21], auto_restore: false, judge: {group_id:21,model_id:'test-judge',prompt:'grade semantically'} } }) }))
    wrapper.unmount()
  })
  it('retries only accounts that were not created before a partial batch failure', async () => {
    vi.mocked(scheduledTests.create).mockResolvedValueOnce({ id: 1 } as any).mockRejectedValueOnce(new Error('failed')).mockResolvedValueOnce({ id: 2 } as any)
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.newPlan(); vm.selectedAccounts = [1, 2]; vm.form.pelican_config.quality.judge = {group_id:21,model_id:'test-judge',prompt:'grade semantically'}; vm.form.model_id = 'test-model'; vm.form.pelican_config.quality.action = 'disable_scheduling'
    await vm.save(); expect(vm.selectedAccounts).toEqual([2]); expect(vm.showForm).toBe(true)
    await vm.save()
    expect(vi.mocked(scheduledTests.create).mock.calls.map(([request]) => request.account_id)).toEqual([1, 2, 2])
    wrapper.unmount()
  })
  it('shows one operation row per round and loads response bodies only on demand', async () => {
    const operation = { id: 10, plan_id: 4, account_id: 1, account_name: 'Test account', status: 'success', error_message: '', quality_action: 'restored', passed_count: 2, total_count: 2, result_ids: [10, 9], pelican_config: { quality: { action: 'remove_groups', remove_group_ids: [21] } } }
    vi.mocked(listQualityOperations).mockResolvedValueOnce({ items: [operation] as any, next_cursor: 10 })
    vi.mocked(scheduledTests.getResult).mockResolvedValue({ id: 10, status: 'success', error_message: '', response_text: '21个。', quality_judgment: { verdict: 'correct', reason: 'same answer', model_id: 'custom-judge' } } as any)
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    expect(wrapper.text()).toContain('qualityOps.operations')
    expect(wrapper.text()).toContain('2 / 2')
    expect(wrapper.text()).toContain('qualityOps.groupsRestored')
    expect(scheduledTests.getResult).not.toHaveBeenCalled()
    await vm.operationDetails(operation)
    expect(scheduledTests.getResult).toHaveBeenCalledWith(4, 10)
    expect(scheduledTests.getResult).not.toHaveBeenCalledWith(4, 9)
    await vm.selectResult(vm.results[1])
    expect(scheduledTests.getResult).toHaveBeenCalledWith(4, 9)
    expect(wrapper.text()).toContain('same answer')
    await vm.moreOperations(); expect(listQualityOperations).toHaveBeenLastCalledWith(10)
    wrapper.unmount()
  })
  it('requires the operator to select a judge model and group', async () => {
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.newPlan(); vm.selectedAccounts = [1]; vm.form.model_id = 'test-model'
    await vm.save(); expect(scheduledTests.create).not.toHaveBeenCalled()
    expect(wrapper.find('[role="alert"]').text()).toContain('qualityOps.configureJudge')
    wrapper.unmount()
  })
  it('saves a state probe rule without a question, reference answer, judge or parallel runs', async () => {
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises()
    vm.selectedAccounts = [1]; vm.form.pelican_config.parallel_count = 3
    await wrapper.find('[data-testid="quality-question-kind"]').setValue('state_probe')
    expect(wrapper.find('[data-testid="quality-probe-hint"]').exists()).toBe(true)
    expect(wrapper.find('#quality-prompt').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('qualityOps.judgeTitle')
    vm.form.pelican_config.quality.remove_group_ids = [21]
    await vm.save()
    const request = vi.mocked(scheduledTests.create).mock.calls[0][0] as any
    expect(request).toMatchObject({ account_id: 1, model_id: 'gpt-6-astra', pelican_config: { question_kind: 'state_probe', prompt: '', parallel_count: 1, quality: { expected_answer: '', action: 'remove_groups', remove_group_ids: [21], auto_restore: false } } })
    expect(request.pelican_config.quality).not.toHaveProperty('judge')
    wrapper.unmount()
  })
  it('switching a probe rule back to candy restores the question and judge fields', async () => {
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.edit({ id: 5, account_id: 1, model_id: 'gpt-6-astra', cron_expression: '*/30 * * * *', enabled: true, max_results: 100, pelican_config: { question_kind: 'state_probe', prompt: '', reasoning_effort: 'high', parallel_count: 1, quality: { expected_answer: '', action: 'disable_scheduling', remove_group_ids: [], auto_restore: true } } })
    await flushPromises()
    expect(wrapper.find('#quality-prompt').exists()).toBe(false)
    await wrapper.find('[data-testid="quality-question-kind"]').setValue('candy')
    expect(wrapper.find('#quality-prompt').exists()).toBe(true)
    expect(vm.form.pelican_config.quality.expected_answer).toBe('21')
    expect(vm.form.pelican_config.quality.judge).toEqual({ group_id: 0, model_id: '', prompt: 'qualityOps.defaultJudgePrompt' })
    await vm.save()
    expect(scheduledTests.update).not.toHaveBeenCalled()
    expect(wrapper.find('[role="alert"]').text()).toContain('qualityOps.configureJudge')
    wrapper.unmount()
  })
  it('tags probe rules and labels probe results as full capability, degraded or inconclusive', async () => {
    const probe = { question_kind: 'state_probe', prompt: '', reasoning_effort: 'high', parallel_count: 1, quality: { expected_answer: '', action: 'disable_scheduling', remove_group_ids: [], auto_restore: false } }
    vi.mocked(listQualityPlans).mockResolvedValue([{ id: 1, account_id: 1, account_name: 'Probe account', model_id: 'gpt-6-astra', cron_expression: '*/30 * * * *', enabled: true, max_results: 100, pelican_config: probe }] as any)
    vi.mocked(scheduledTests.listResults).mockResolvedValue([
      { id: 7, status: 'failed', error_message: 'state_degraded', pelican_config: probe },
      { id: 6, status: 'failed', error_message: 'state_probe_inconclusive: network', pelican_config: probe },
      { id: 5, status: 'success', error_message: '', pelican_config: probe }
    ] as any)
    vi.mocked(scheduledTests.getResult).mockResolvedValue({ id: 7, status: 'failed', error_message: 'state_degraded', response_text: '判定：降智', pelican_config: probe, quality_judgment: { verdict: 'incorrect', reason: 'new ticket' } } as any)
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    expect(wrapper.find('[data-testid="quality-probe-tag"]').exists()).toBe(true)
    expect(wrapper.find('.rule-warning').exists()).toBe(false)
    await vm.history(vm.plans[0]); await flushPromises()
    const labels = wrapper.findAll('.result-navigation button').map(button => button.text())
    expect(labels[0]).toContain('qualityOps.probeDegraded')
    expect(labels[1]).toContain('qualityOps.probeInconclusive')
    expect(labels[2]).toContain('qualityOps.probeHealthy')
    expect(wrapper.find('[data-testid="quality-result-badge"]').text()).toBe('qualityOps.probeDegraded')
    expect(wrapper.find('pre').text()).toContain('判定：降智')
    expect(wrapper.find('.answer-reference').exists()).toBe(false)
    expect(wrapper.find('.judge-reason').exists()).toBe(false)
    wrapper.unmount()
  })
  it('offers enable BPS only for probe rules and saves the normalized BPS policy', async () => {
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises(); vm.selectedAccounts = [1]
    expect(wrapper.find('[data-testid="quality-action-enable-bps"]').exists()).toBe(false)
    await wrapper.find('[data-testid="quality-question-kind"]').setValue('state_probe')
    expect((wrapper.find('[data-testid="quality-auto-restore"]').element as HTMLInputElement).checked).toBe(false)
    await wrapper.find('[data-testid="quality-action-enable-bps"]').setValue(true)
    expect(wrapper.find('[data-testid="quality-bps-settings"]').exists()).toBe(true)
    expect((wrapper.find('[data-testid="quality-bps-auto-disable"]').element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.find('[data-testid="quality-auto-restore"]').exists()).toBe(false)
    const checked = (id: string) => (wrapper.find(`[data-testid="quality-bps-${id}"]`).element as HTMLInputElement).checked
    expect([checked('omit_unsupported_tools'), checked('ignore_images'), checked('ignore_encrypted_content'), checked('auto_disable_on_403')]).toEqual([true, false, true, false])
    expect(checked('auto_recover_on_403')).toBe(false)
    expect((wrapper.find('[data-testid="quality-bps-auto_recover_on_403"]').element as HTMLInputElement).disabled).toBe(true)
    expect(wrapper.find('[data-testid="model-selector"]').text()).toBe('gpt-6-astra,gpt-5.6-sol,gpt-5.6-terra')
    expect(wrapper.find('[data-testid="quality-bps-require-all"]').exists()).toBe(false)
    expect((wrapper.find('[data-testid="quality-bps-pass-threshold"]').element as HTMLInputElement).value).toBe('2')
    expect(wrapper.find('[data-testid="quality-bps-hold-on-usage"]').exists()).toBe(false)
    await wrapper.find('[data-testid="quality-bps-threshold"]').setValue('3')
    await wrapper.find('[data-testid="quality-bps-usage"]').setValue('80')
    expect(checked('hold-on-usage')).toBe(true)
    await wrapper.find('[data-testid="quality-bps-hold-on-usage"]').setValue(false)
    await wrapper.find('[data-testid="quality-bps-pass-threshold"]').setValue('3')
    await wrapper.find('[data-testid="quality-bps-require-all"]').setValue(true)
    expect(wrapper.find('[data-testid="quality-bps-hold-on-usage"]').exists()).toBe(false)
    await wrapper.find('[data-testid="quality-bps-ignore_images"]').setValue(true)
    await wrapper.find('[data-testid="quality-bps-astra-only"]').trigger('click')
    await wrapper.find('[data-testid="quality-bps-auto_move_on_403"]').setValue(true)
    await vm.save()
    expect(scheduledTests.create).not.toHaveBeenCalled()
    expect(wrapper.find('[role="alert"]').text()).toContain('qualityOps.bpsTargetGroupRequired')
    await wrapper.find('[data-testid="quality-bps-target-group"]').setValue('21')
    await wrapper.find('[data-testid="quality-bps-auto_disable_on_403"]').setValue(true)
    await wrapper.find('[data-testid="quality-bps-auto_recover_on_403"]').setValue(true)
    const interval = wrapper.get<HTMLInputElement>('[data-testid="quality-bps-recovery-interval"]')
    expect(interval.element.value).toBe('60')
    await interval.setValue('0')
    await vm.save()
    expect(scheduledTests.create).not.toHaveBeenCalled()
    expect(wrapper.find('[role="alert"]').text()).toContain('admin.accounts.openai.excelBPS403RecoveryIntervalInvalid')
    await interval.setValue('360')
    vm.form.pelican_config.quality.remove_group_ids = [21]
    await vm.save()
    const request = vi.mocked(scheduledTests.create).mock.calls[0][0] as any
    expect(request.pelican_config.quality).toEqual({ expected_answer: '', action: 'enable_bps', remove_group_ids: [], auto_restore: true, bps: {
      failure_threshold: 3, usage_percent: 80, require_all: true, all_models: false, models: ['gpt-6-astra'],
      omit_unsupported_tools: true, ignore_images: true, ignore_encrypted_content: true, auto_disable_on_403: true,
      auto_recover_on_403: true, recovery_interval_minutes: 360,
      auto_move_on_403: true, target_group_id: 21, session_proxy: false, proxy_source: '', cache_creation_as_input: false,
      pass_threshold: 3, hold_on_usage: false } })
    wrapper.unmount()
  })
  it('validates BPS triggers and drops BPS when a rule switches back to candy', async () => {
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises(); vm.selectedAccounts = [1]
    await wrapper.find('[data-testid="quality-question-kind"]').setValue('state_probe')
    await wrapper.find('[data-testid="quality-action-enable-bps"]').setValue(true)
    const alert = async () => { await vm.save(); return wrapper.find('[role="alert"]').text() }
    await wrapper.find('[data-testid="quality-bps-threshold"]').setValue('0')
    expect(await alert()).toContain('qualityOps.bpsTriggerRequired')
    await wrapper.find('[data-testid="quality-bps-threshold"]').setValue('1.5')
    expect(await alert()).toContain('qualityOps.bpsCountInvalid')
    await wrapper.find('[data-testid="quality-bps-threshold"]').setValue('2')
    await wrapper.find('[data-testid="quality-bps-usage"]').setValue('101')
    expect(await alert()).toContain('qualityOps.bpsUsageInvalid')
    await wrapper.find('[data-testid="quality-bps-usage"]').setValue('0')
    await wrapper.find('[data-testid="quality-bps-pass-threshold"]').setValue('0')
    expect(await alert()).toContain('qualityOps.bpsPassCountInvalid')
    await wrapper.find('[data-testid="quality-bps-pass-threshold"]').setValue('101')
    expect(await alert()).toContain('qualityOps.bpsPassCountInvalid')
    await wrapper.find('[data-testid="quality-bps-pass-threshold"]').setValue('1')
    vm.form.pelican_config.quality.bps.models = []
    expect(await alert()).toContain('qualityOps.bpsModelsRequired')
    await wrapper.find('[data-testid="quality-bps-all-models"]').setValue(true)
    expect(wrapper.find('[data-testid="quality-bps-models"]').exists()).toBe(false)
    expect(scheduledTests.create).not.toHaveBeenCalled()
    await wrapper.find('[data-testid="quality-question-kind"]').setValue('candy')
    expect(vm.form.pelican_config.quality.action).toBe('remove_groups')
    expect(wrapper.find('[data-testid="quality-action-enable-bps"]').exists()).toBe(false)
    vm.form.pelican_config.quality.judge = { group_id: 21, model_id: 'test-judge', prompt: 'grade semantically' }
    vm.form.pelican_config.quality.remove_group_ids = [21]
    await vm.save()
    const request = vi.mocked(scheduledTests.create).mock.calls[0][0] as any
    expect(request.pelican_config.quality).toMatchObject({ action: 'remove_groups', remove_group_ids: [21] })
    expect(request.pelican_config.quality.bps).toBeUndefined()
    wrapper.unmount()
  })
  it('loads saved BPS rules, summarizes their trigger and labels counted failures', async () => {
    const probe = { question_kind: 'state_probe', prompt: '', reasoning_effort: 'high', parallel_count: 1, quality: { expected_answer: '', action: 'enable_bps', remove_group_ids: [], auto_restore: true,
      bps: { failure_threshold: 2, usage_percent: 80, require_all: false, all_models: true, models: null, omit_unsupported_tools: false, ignore_images: true, ignore_encrypted_content: false,
        auto_disable_on_403: true, auto_move_on_403: false, target_group_id: 0, session_proxy: true, proxy_source: 'ip_pool', cache_creation_as_input: true } } }
    vi.mocked(listQualityPlans).mockResolvedValue([{ id: 8, account_id: 1, account_name: 'BPS account', model_id: 'gpt-6-astra', cron_expression: '*/30 * * * *', enabled: true, max_results: 100, pelican_config: probe }] as any)
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    expect(wrapper.find('.rule-target').text()).toBe('qualityOps.enableBPSShort（qualityOps.bpsTriggerCount / qualityOps.bpsTriggerUsage）')
    expect(vm.actionLabel('failure_counted:1/2')).toBe('qualityOps.outcomes.failure_counted_n')
    expect(vm.actionLabel('failure_counted:2/2')).toBe('qualityOps.outcomes.failure_counted_usage')
    expect([vm.tone('failure_counted:1/2'), vm.tone('bps_enabled'), vm.tone('bps_blocked_403')]).toEqual(['tone-muted', 'tone-success', 'tone-warning'])
    expect(vm.actionLabel('restore_counted:1/3')).toBe('qualityOps.outcomes.restore_counted_n')
    expect(vm.tone('restore_counted:1/3')).toBe('tone-muted')
    expect(vm.actionExplanation('restore_counted:1/3')).toBe('qualityOps.actionHelp.restore_counted')
    vm.edit(vm.plans[0]); await flushPromises()
    // 早先保存、没有满血关闭次数的规则后端按 1 次处理。
    expect(vm.form.pelican_config.quality.bps).toMatchObject({ all_models: true, models: ['gpt-6-astra', 'gpt-5.6-sol', 'gpt-5.6-terra'], target_group_id: -1, proxy_source: 'ip_pool', ignore_images: true, pass_threshold: 1 })
    expect((wrapper.find('[data-testid="quality-bps-pass-threshold"]').element as HTMLInputElement).value).toBe('1')
    expect(wrapper.find('[data-testid="quality-bps-models"]').exists()).toBe(false)
    await wrapper.find('[data-testid="quality-bps-all-models"]').setValue(false)
    expect(wrapper.find('[data-testid="quality-bps-models"]').exists()).toBe(true)
    expect((wrapper.find('[data-testid="quality-bps-auto-disable"]').element as HTMLInputElement).checked).toBe(true)
    await wrapper.find('[data-testid="quality-bps-auto-disable"]').setValue(false)
    await vm.save()
    const body = vi.mocked(scheduledTests.update).mock.calls[0][1] as any
    expect(vi.mocked(scheduledTests.update).mock.calls[0][0]).toBe(8)
    expect(body.pelican_config.quality).toMatchObject({ action: 'enable_bps', auto_restore: false, remove_group_ids: [], bps: {
      all_models: false, models: ['gpt-6-astra', 'gpt-5.6-sol', 'gpt-5.6-terra'], target_group_id: 0, session_proxy: true, proxy_source: 'ip_pool', cache_creation_as_input: true } })
    wrapper.unmount()
  })
  it('defaults BPS auto-disable only for new rules and places the toggle where the BPS settings are', async () => {
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    vm.newPlan(); await flushPromises()
    await wrapper.find('[data-testid="quality-question-kind"]').setValue('state_probe')
    await wrapper.find('[data-testid="quality-action-enable-bps"]').setValue(true)
    expect(vm.form.pelican_config.quality.auto_restore).toBe(true)
    await wrapper.find('input[type="radio"][value="remove_groups"]').setValue(true)
    expect(vm.form.pelican_config.quality.auto_restore).toBe(false)
    expect(wrapper.find('[data-testid="quality-bps-auto-disable"]').exists()).toBe(false)
    wrapper.unmount()

    const saved = rules(); saved[0].pelican_config!.quality!.action = 'enable_bps'
    vi.mocked(listQualityPlans).mockResolvedValue(saved)
    const view = mountView(); await flushPromises(); const page = view.vm as any
    page.edit(page.plans.find((plan: ScheduledTestPlan) => plan.id === 2)); await flushPromises()
    await view.find('[data-testid="quality-action-enable-bps"]').setValue(true)
    expect((view.find('[data-testid="quality-bps-auto-disable"]').element as HTMLInputElement).checked).toBe(false)
    expect(page.form.pelican_config.quality.auto_restore).toBe(false)
    page.edit(page.plans.find((plan: ScheduledTestPlan) => plan.id === 1)); await flushPromises()
    expect(page.initialForm).toBe(page.formSnapshot())
    page.showForm = false; await flushPromises()

    page.selectedRuleIds = [1]; await flushPromises()
    await view.get('[data-testid="quality-bulk-edit"]').trigger('click'); await flushPromises()
    // 只改「自动恢复」：BPS 规则给出满血关闭条件；各规则开启条件不同，始终给出「用量仍高时先不关」。
    await view.get('[data-testid="quality-bulk-field-restore"]').setValue(true)
    expect(view.find('[data-testid="quality-auto-restore"]').exists()).toBe(false)
    expect(view.find('[data-testid="quality-bps-settings"]').exists()).toBe(false)
    expect((view.find('[data-testid="quality-bps-auto-disable"]').element as HTMLInputElement).checked).toBe(false)
    expect(view.find('[data-testid="quality-bps-restore-options"]').exists()).toBe(false)
    await view.find('[data-testid="quality-bps-auto-disable"]').setValue(true)
    expect(view.find('[data-testid="quality-bps-hold-on-usage"]').exists()).toBe(true)
    await view.find('[data-testid="quality-bps-pass-threshold"]').setValue('4')
    await view.get('[data-testid="quality-bulk-field-action"]').setValue(true)
    expect(view.find('[data-testid="quality-bps-settings"]').exists()).toBe(true)
    expect(view.findAll('[data-testid="quality-bps-auto-disable"]')).toHaveLength(1)
    expect((view.find('[data-testid="quality-bps-auto-disable"]').element as HTMLInputElement).checked).toBe(true)
    expect(view.find('[data-testid="quality-bps-hold-on-usage"]').exists()).toBe(false)
    await view.get('#quality-rule-form').trigger('submit'); await flushPromises()
    expect(vi.mocked(scheduledTests.update)).toHaveBeenCalledTimes(1)
    const [id, body] = vi.mocked(scheduledTests.update).mock.calls[0] as any
    expect(id).toBe(1)
    expect(body.pelican_config.quality).toMatchObject({ action: 'enable_bps', auto_restore: true, bps: { pass_threshold: 4, hold_on_usage: true } })
    view.unmount()
  })
  it('renders returned model content as text', async () => {
    vi.mocked(scheduledTests.listResults).mockResolvedValue([{ id: 4, status: 'failed', error_message: 'answer_mismatch', quality_action: 'groups_removed' }] as any)
    vi.mocked(scheduledTests.getResult).mockResolvedValue({ id: 4, status: 'failed', error_message: 'answer_mismatch', response_text: '<img src=x onerror=alert(1)>', quality_action: 'groups_removed' } as any)
    const wrapper = mountView(); await flushPromises(); const vm = wrapper.vm as any
    await vm.history({ id: 1 })
    expect(wrapper.find('pre').text()).toContain('<img')
    expect(wrapper.find('pre img').exists()).toBe(false)
    wrapper.unmount()
  })
})
