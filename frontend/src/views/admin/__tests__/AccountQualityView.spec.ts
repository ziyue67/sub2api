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
vi.mock('@/api/admin/accounts', () => ({ list: vi.fn().mockResolvedValue({ items: [{ id: 1, name: 'Test account' }], total: 1 }) }))
vi.mock('@/api/admin/groups', () => ({ getModelAllowlistCandidates: vi.fn().mockResolvedValue(["test-judge"]), getAllIncludingInactive: vi.fn().mockResolvedValue([{ id: 21, name: 'Quality pool', status:'active' }]) }))
const mountView = () => mount(AccountQualityView, { global: { plugins: [createPinia()], stubs: { Teleport: true, AppLayout: { template: '<main><slot /></main>' } } } })
describe('quality operations', () => {
  beforeEach(() => { vi.clearAllMocks(); vi.mocked(scheduledTests.update).mockReset(); vi.mocked(accountsAPI.list).mockReset().mockResolvedValue({ items: [{ id: 1, name: 'Test account' }], total: 1 } as any); vi.mocked(listQualityPlans).mockResolvedValue([]); vi.mocked(listQualityOperations).mockResolvedValue({items:[],next_cursor:0}) })
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
    expect(wrapper.get('[data-testid="quality-bulk-edit"]').attributes('disabled')).toBeDefined()
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
    expect(accountsAPI.list).not.toHaveBeenCalled()
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
