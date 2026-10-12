import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import Panel from '../HarvestPoolSchedulingPanel.vue'
const mocks = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn() }))
vi.mock('@/api/admin/astraGateway', async importOriginal => ({ ...await importOriginal<typeof import('@/api/admin/astraGateway')>(), getAstraGateway: mocks.get, saveAstraGateway: mocks.save }))
vi.mock('@/api/admin/groups', () => ({ getAllIncludingInactive: vi.fn().mockResolvedValue([{ id: 7, name: 'Test group' }]) }))
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
const value = () => ({ account_scheduling: false, scheduling_mode: 'account', scheduling_group_ids: [], cookie_pool: { enabled: false, source_account_ids: [299], target_account_ids: [300] }, ws_session: { enabled: false, account_ids: [300] }, revision: 'one' })
beforeEach(() => { vi.clearAllMocks(); mocks.get.mockResolvedValue(value()); mocks.save.mockImplementation(v => Promise.resolve({ ...v, revision: 'two' })) })
describe('Harvest pool scheduling', () => {
  it.each(['model', 'account', 'groups'] as const)('exposes and saves %s scheduling without expanding borrowing', async mode => {
    const w = mount(Panel)
    await flushPromises()
    expect(w.get('[data-testid="pool-scheduling-panel"]').isVisible()).toBe(true)
    expect(w.find('[data-testid="scheduling-records"]').exists()).toBe(false)
    await w.get('[data-testid="account-scheduling-toggle"]').trigger('click')
    await w.get('[data-testid="scheduling-mode"]').setValue(mode)
    await flushPromises()
    if (mode === 'groups') await w.get('[data-testid="scheduling-groups"] input').setValue(true)
    await w.get('[data-testid="save-scheduling"]').trigger('click')
    await flushPromises()
    expect(mocks.save).toHaveBeenCalledWith(expect.objectContaining({
      ticket_pool_scheduling: true, scheduling_mode: mode,
      scheduling_group_ids: mode === 'groups' ? [7] : [],
      cookie_pool: expect.objectContaining({ enabled: false, source_account_ids: [299], target_account_ids: [300] }),
    }))
    w.unmount()
  })

  it('preserves edits when saving fails and keeps routing settings', async () => {
    mocks.save.mockRejectedValueOnce(new Error('revision conflict'))
    const w = mount(Panel)
    await flushPromises()
    await w.get('[data-testid="account-scheduling-toggle"]').trigger('click')
    await w.get('[data-testid="save-scheduling"]').trigger('click')
    await flushPromises()
    expect(mocks.save).toHaveBeenCalledWith(expect.objectContaining({ revision: 'one', ws_session: expect.objectContaining({ account_ids: [300] }) }))
    expect(w.get('[role="alert"]').text()).toContain('saveError')
    expect(w.get('[data-testid="account-scheduling-toggle"]').attributes('aria-checked')).toBe('true')
    w.unmount()
  })
})
