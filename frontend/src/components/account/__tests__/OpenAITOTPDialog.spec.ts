import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import OpenAITOTPDialog from '../OpenAITOTPDialog.vue'

const api = vi.hoisted(() => ({ status: vi.fn(), rotate: vi.fn(), verify: vi.fn(), export: vi.fn() }))
vi.mock('@/api/admin/openaiTotp', () => ({ getTOTPRotation: api.status, rotateTOTP: api.rotate, verifyTOTP: api.verify, exportTOTP: api.export }))
vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('qrcode', () => ({ default: { toDataURL: vi.fn().mockResolvedValue('data:image/png;base64,synthetic') } }))
let wrapper: VueWrapper
const dialog = { props: ['show'], emits: ['close'], template: '<div v-if="show"><slot/><slot name="footer"/><button data-testid="close" @click="$emit(\'close\')">close</button></div>' }
const exported = { email: 'owner@example.test', secret: 'SYNTHETIC-SECRET', otpauth_uri: 'otpauth://synthetic', source: 'current', state: 'succeeded' }
async function open() {
  wrapper = mount(OpenAITOTPDialog, { props: { accountId: 42, accountName: 'Test account', configured: true }, global: { stubs: { BaseDialog: dialog } } })
  await wrapper.get('[data-testid="totp-manage"]').trigger('click'); await flushPromises()
}
beforeEach(() => { vi.clearAllMocks(); vi.useFakeTimers(); api.status.mockResolvedValue(null); api.export.mockResolvedValue(exported) })
afterEach(() => { wrapper?.unmount(); vi.useRealTimers(); vi.restoreAllMocks() })

describe('2FA rotation and credential export', () => {
  it('does not fetch secrets until explicitly requested and omits password by default', async () => {
    await open(); expect(api.export).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="totp-export"]').trigger('click'); await flushPromises()
    expect(api.export).toHaveBeenCalledWith(42, 'current', false)
    expect(wrapper.get('[data-testid="totp-export-result"]').exists()).toBe(true)
    await wrapper.get('[data-testid="close"]').trigger('click'); await flushPromises()
    expect(wrapper.html()).not.toContain(exported.secret)
    expect(vi.getTimerCount()).toBe(0)
  })
  it('never offers another rotation while activation is uncertain', async () => {
    api.status.mockResolvedValue({ task_id: 7, account_id: 42, state: 'uncertain', action: 'rotate', has_candidate: true })
    await open()
    expect(wrapper.get('[data-testid="totp-rotate"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="totp-verify-new"]').trigger('click'); await flushPromises()
    expect(api.verify).toHaveBeenCalledWith(42, 7, 'verify_new')
    expect(api.rotate).not.toHaveBeenCalled()
  })
  it('exports the recovery candidate with an explicit source', async () => {
    api.status.mockResolvedValue({ task_id: 7, state: 'uncertain', has_candidate: true })
    await open(); await wrapper.get('[data-testid="totp-export"]').trigger('click'); await flushPromises()
    expect(api.export).toHaveBeenCalledWith(42, 'candidate', false)
  })
  it('ignores a late secret response after closing the dialog', async () => {
    let resolve!: (value: typeof exported) => void
    api.export.mockImplementation(() => new Promise(r => { resolve = r }))
    await open(); await wrapper.get('[data-testid="totp-export"]').trigger('click')
    await wrapper.get('[data-testid="close"]').trigger('click')
    resolve(exported); await flushPromises()
    await wrapper.get('[data-testid="totp-manage"]').trigger('click'); await flushPromises()
    expect(wrapper.html()).not.toContain(exported.secret)
    expect(wrapper.find('[data-testid="totp-export-result"]').exists()).toBe(false)
  })
  it('requires an explicit confirmation before queuing a replacement', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    await open(); await wrapper.get('[data-testid="totp-rotate"]').trigger('click'); expect(api.rotate).not.toHaveBeenCalled()
    confirm.mockReturnValue(true); api.rotate.mockResolvedValue({ task_id: 7, state: 'queued' })
    await wrapper.get('[data-testid="totp-rotate"]').trigger('click'); await flushPromises()
    expect(api.rotate).toHaveBeenCalledOnce(); expect(wrapper.get('[data-testid="totp-rotate"]').attributes('disabled')).toBeDefined()
  })
})
