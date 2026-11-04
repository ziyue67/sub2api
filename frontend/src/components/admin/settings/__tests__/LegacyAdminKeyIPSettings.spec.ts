import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import LegacyAdminKeyIPSettings from '../LegacyAdminKeyIPSettings.vue'

const api = vi.hoisted(() => ({ getLegacyAdminKeyIPPolicy: vi.fn(), updateLegacyAdminKeyIPPolicy: vi.fn() }))
vi.mock('@/api/admin/legacyAdminKeyIP', () => api)
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: (fn: () => unknown) => fn() }), isStepUpCancelled: () => false }))
beforeEach(() => {
  vi.clearAllMocks()
  api.getLegacyAdminKeyIPPolicy.mockResolvedValue({ enabled: false, allowed_ips: [], current_ip: '192.0.2.10' })
})
describe('Legacy admin key IP policy', () => {
  it('does not add the current IP or save without an explicit edit', async () => {
    const w = mount(LegacyAdminKeyIPSettings)
    await flushPromises()
    expect((w.get('[data-test="ips"]').element as HTMLTextAreaElement).value).toBe('')
    expect(w.get('[data-test="save"]').attributes('disabled')).toBeDefined()
    expect(api.updateLegacyAdminKeyIPPolicy).not.toHaveBeenCalled()
  })
  it('rejects an enabled empty list and more than 100 entries', async () => {
    const w = mount(LegacyAdminKeyIPSettings)
    await flushPromises()
    await w.get('[data-test="enabled"]').setValue(true)
    await w.get('[data-test="save"]').trigger('click')
    expect(api.updateLegacyAdminKeyIPPolicy).not.toHaveBeenCalled()
    await w.get('[data-test="ips"]').setValue(Array(101).fill('192.0.2.1').join('\n'))
    await w.get('[data-test="save"]').trigger('click')
    expect(api.updateLegacyAdminKeyIPPolicy).not.toHaveBeenCalled()
  })
  it('saves the whole policy, keeps the old saved state on failure, and reloads canonical entries', async () => {
    const w = mount(LegacyAdminKeyIPSettings)
    await flushPromises()
    await w.get('[data-test="enabled"]').setValue(true)
    await w.get('[data-test="ips"]').setValue(' 192.0.2.1 \n2001:db8::1/64')
    api.updateLegacyAdminKeyIPPolicy.mockRejectedValueOnce(new Error('network'))
    await w.get('[data-test="save"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="save"]').attributes('disabled')).toBeUndefined()
    api.updateLegacyAdminKeyIPPolicy.mockResolvedValueOnce({ enabled: true, allowed_ips: ['192.0.2.1', '2001:db8::/64'], current_ip: '192.0.2.10' })
    await w.get('[data-test="save"]').trigger('click'); await flushPromises()
    expect(api.updateLegacyAdminKeyIPPolicy).toHaveBeenLastCalledWith({ enabled: true, allowed_ips: ['192.0.2.1', '2001:db8::1/64'] })
    expect((w.get('[data-test="ips"]').element as HTMLTextAreaElement).value).toBe('192.0.2.1\n2001:db8::/64')
    expect(w.get('[data-test="save"]').attributes('disabled')).toBeDefined()
  })
  it('does not offer a save action after initial loading fails', async () => {
    api.getLegacyAdminKeyIPPolicy.mockRejectedValueOnce(new Error('offline'))
    const w = mount(LegacyAdminKeyIPSettings); await flushPromises()
    expect(w.find('[data-test="save"]').exists()).toBe(false)
    expect(w.find('[role="alert"]').exists()).toBe(true)
  })
})
