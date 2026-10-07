import { beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import PrioritySchedulingView from '../PrioritySchedulingView.vue'
import { getPriorityConfig, getPrioritySnapshot, savePriorityConfig, type PrioritySchedulingConfig, type PriorityCandidate } from '@/api/admin/priorityScheduling'
const state = vi.hoisted(() => ({ auth: null as any }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => state.auth }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/components/admin/operations/SmartOpsNav.vue', () => ({ default: { template: '<nav />' } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/priorityScheduling', () => ({ getPriorityConfig: vi.fn(), getPrioritySnapshot: vi.fn(), savePriorityConfig: vi.fn() }))
const config: PrioritySchedulingConfig = { oauth_quota_priority: false, oauth_quota_threshold: 90, balance_protocols: true, enabled: false, mode: 'balanced', group_ids: [], models: [], window_minutes: 60, min_samples: 5, target_ttft_ms: 3000, max_load_percent: 80, min_quality_percent: 90, quality_max_age_hours: 24, quality_weight: 30, latency_weight: 25, load_weight: 25, cost_weight: 20 }
beforeEach(() => { vi.resetAllMocks(); state.auth = reactive({ user: { id: 1, role: 'admin' } }); vi.mocked(getPriorityConfig).mockResolvedValue({ ...config }); vi.mocked(getPrioritySnapshot).mockResolvedValue(null); vi.mocked(savePriorityConfig).mockImplementation(async c => c) })
describe('priority scheduling', () => {
  it('defaults legacy settings off and saves the quota cutoff and explicit disable', async () => {
    const legacy = { ...config }; delete legacy.oauth_quota_priority; delete legacy.oauth_quota_threshold
    vi.mocked(getPriorityConfig).mockResolvedValue(legacy)
    const wrapper = mount(PrioritySchedulingView); await flushPromises()
    expect((wrapper.get('[data-testid="oauth-quota-priority"]').element as HTMLInputElement).checked).toBe(false)
    expect(wrapper.find('[data-testid="oauth-quota-threshold"]').exists()).toBe(false)
    await wrapper.get('[data-testid="oauth-quota-priority"]').setValue(true)
    expect((wrapper.get('[data-testid="oauth-quota-threshold"]').element as HTMLInputElement).value).toBe('90')
    await wrapper.get('[data-testid="oauth-quota-threshold"]').setValue(85)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(savePriorityConfig).toHaveBeenLastCalledWith({ ...config, oauth_quota_priority: true, oauth_quota_threshold: 85 })
    await wrapper.get('[data-testid="oauth-quota-priority"]').setValue(false)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(savePriorityConfig).toHaveBeenLastCalledWith({ ...config, oauth_quota_priority: false, oauth_quota_threshold: 85 })
    wrapper.unmount()
  })

  it('can retain strict BPS preference explicitly', async () => {
    const wrapper = mount(PrioritySchedulingView); await flushPromises()
    await wrapper.get('[data-testid="balance-protocols"]').setValue(false)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(savePriorityConfig).toHaveBeenCalledWith({ ...config, balance_protocols: false })
    wrapper.unmount()
  })
  it('shows capacity weights, group bindings and exploration without treating scores as selections', async () => {
    const candidate: PriorityCandidate = { account_id: 1, account_name: 'Mock OAuth', oauth_quota_role: 'standby', score: 75, tier: 'insufficient', reasons: [], priority: 1, concurrency: 20, load_factor: 10000, load_percent: 0, waiting: 0, rate: 0.1, revenue: 0, theoretical_cost: 0, profit: null, margin: null, economics_source: 'unknown', profit_samples: 0, samples: 0, p90_ttft_ms: 0, quality_passed: 0, quality_samples: 0, bound_groups: 2, selection_weight: 12.345, exploration_eligible: true }
    vi.mocked(getPrioritySnapshot).mockResolvedValue({ at: '2026-09-29T12:00:00Z', model: 'gpt-test', group_id: 11, mode: 'profit', selection_policy: 'capacity_first', history_ready: true, candidates: [candidate] })
    const wrapper = mount(PrioritySchedulingView); await flushPromises()
    expect(wrapper.text()).toContain('priorityScheduling.boundGroups: 2')
    expect(wrapper.text()).toContain('priorityScheduling.selectionWeight: 12.35')
    expect(wrapper.text()).toContain('priorityScheduling.explorationEligible')
    // Rolling upgrades can still return the previous snapshot shape.
    const legacy = { ...candidate }; delete legacy.selection_weight; delete legacy.bound_groups; delete legacy.exploration_eligible
    vi.mocked(getPrioritySnapshot).mockResolvedValue({ at: '2026-09-29T12:00:00Z', model: 'gpt-test', group_id: 11, mode: 'profit', history_ready: true, candidates: [legacy] })
    await (wrapper.vm as any).refresh(); await flushPromises()
    expect(wrapper.text()).toContain('Mock OAuth')
    expect(wrapper.text()).toContain('priorityScheduling.oauthQuotaRoles.standby')
    expect(wrapper.text()).not.toContain('priorityScheduling.selectionWeight:')
    wrapper.unmount()
  })
  it('removes purchase-cost controls and saves without legacy Teams settings', async () => {
    vi.mocked(getPriorityConfig).mockResolvedValue({ ...config, teams: { enabled: true, cost_cny: 50 } } as PrioritySchedulingConfig)
    const wrapper = mount(PrioritySchedulingView); await flushPromises()
    expect(wrapper.find('[data-testid="teams-recovery"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="teams-window-source"]').exists()).toBe(false)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(savePriorityConfig).toHaveBeenCalledWith(config)
    wrapper.unmount()
  })
  it('saves enabled strategy and exact group/model scope', async () => {
    const wrapper = mount(PrioritySchedulingView); await flushPromises()
    await wrapper.get('[data-testid="enabled"]').setValue(true)
    await wrapper.get('[data-testid="mode"]').setValue('profit')
    await wrapper.get('[data-testid="groups"]').setValue('5, 7, 5')
    await wrapper.get('textarea').setValue('gpt-test\ngpt-other\n')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(savePriorityConfig).toHaveBeenCalledWith({ ...config, enabled: true, mode: 'profit', group_ids: [5, 7], models: ['gpt-test', 'gpt-other'] })
    expect(wrapper.text()).toContain('priorityScheduling.saved'); wrapper.unmount()
  })
  it('rejects invalid groups without sending a write', async () => {
    const wrapper = mount(PrioritySchedulingView); await flushPromises()
    await wrapper.get('[data-testid="groups"]').setValue('5, invalid')
    await wrapper.get('form').trigger('submit'); expect(savePriorityConfig).not.toHaveBeenCalled(); wrapper.unmount()
  })
  it('preserves draft when score refresh fails and displays the error', async () => {
    const wrapper = mount(PrioritySchedulingView); await flushPromises()
    await wrapper.get('[data-testid="mode"]').setValue('custom')
    vi.mocked(getPrioritySnapshot).mockRejectedValueOnce(new Error('offline'))
    await (wrapper.vm as any).refresh(); expect(wrapper.get('[role="alert"]').text()).toContain('priorityScheduling.error')
    expect((wrapper.get('[data-testid="mode"]').element as HTMLSelectElement).value).toBe('custom'); wrapper.unmount()
  })
  it('ignores in-flight settings after logout', async () => {
    let resolve!: (c: PrioritySchedulingConfig) => void
    vi.mocked(getPriorityConfig).mockReturnValueOnce(new Promise(r => { resolve = r }))
    const wrapper = mount(PrioritySchedulingView); state.auth.user = null; resolve(config); await flushPromises()
    expect(wrapper.find('form').exists()).toBe(false); wrapper.unmount()
  })
})

describe('priority history refresh', () => {
  it('distinguishes a failed refresh from an empty successful history', async () => {
    vi.mocked(getPrioritySnapshot).mockResolvedValue({ at: '2026-10-06T00:00:00Z', evaluated_at: '2026-10-06T00:01:00Z', model: 'test', group_id: 11, mode: 'balanced', history_ready: false, history_status: 'error', history_error: 'timeout', candidates: [] })
    const wrapper = mount(PrioritySchedulingView); await flushPromises()
    expect(wrapper.text()).toContain('priorityScheduling.historyStates.error')
    expect(wrapper.text()).not.toContain('priorityScheduling.historyPending')
    vi.mocked(getPrioritySnapshot).mockResolvedValue({ at: '2026-10-06T00:00:00Z', model: 'test', group_id: 11, mode: 'balanced', history_ready: true, history_status: 'ready', candidates: [] })
    await (wrapper.vm as any).refresh()
    expect(wrapper.text()).not.toContain('priorityScheduling.historyStates.error')
    wrapper.unmount()
  })
  it.each([true, false])('picks up history while refreshing=%s and stops polling once complete', async (refreshing) => {
    vi.useFakeTimers()
    try {
      const snapshot = { at: '2026-10-06T00:00:00Z', model: 'test', group_id: 11, mode: 'balanced', history_ready: false, history_status: 'loading' as const, history_refreshing: refreshing, candidates: [] }
      vi.mocked(getPrioritySnapshot).mockResolvedValueOnce(snapshot).mockResolvedValue({ ...snapshot, history_ready: true, history_status: 'ready', history_refreshing: false })
      const wrapper = mount(PrioritySchedulingView); await flushPromises()
      await vi.advanceTimersByTimeAsync(1000); await flushPromises()
      expect(getPrioritySnapshot).toHaveBeenCalledTimes(2)
      await vi.advanceTimersByTimeAsync(10000)
      expect(getPrioritySnapshot).toHaveBeenCalledTimes(2)
      wrapper.unmount()
    } finally { vi.useRealTimers() }
  })
  it('bounds background reads and cancels them on logout', async () => {
    vi.useFakeTimers()
    try {
      vi.mocked(getPrioritySnapshot).mockResolvedValue({ at: '2026-10-06T00:00:00Z', model: 'test', group_id: 11, mode: 'balanced', history_ready: false, history_refreshing: true, candidates: [] })
      const wrapper = mount(PrioritySchedulingView); await flushPromises()
      await vi.advanceTimersByTimeAsync(10000)
      expect(getPrioritySnapshot).toHaveBeenCalledTimes(4)
      await (wrapper.vm as any).refresh()
      state.auth.user = null
      await vi.advanceTimersByTimeAsync(10000)
      expect(getPrioritySnapshot).toHaveBeenCalledTimes(5)
      expect(wrapper.text()).not.toContain('2026')
      wrapper.unmount()
    } finally { vi.useRealTimers() }
  })
})
