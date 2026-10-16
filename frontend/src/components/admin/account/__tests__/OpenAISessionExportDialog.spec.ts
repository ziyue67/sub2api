import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import OpenAISessionExportDialog from '../OpenAISessionExportDialog.vue'
import type { Account } from '@/types'
import type { StepUpController } from '@/composables/useStepUp'

const { exportSession } = vi.hoisted(() => ({ exportSession: vi.fn() }))
vi.mock('@/api/admin/accounts', () => ({ exportOpenAISession: exportSession }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: {
  name: 'TotpStepUpDialog',
  props: ['controller'],
  template: `<div v-if="controller.visible.value" data-testid="step-up">
    <button data-testid="verify" @click="controller.onVerified()">Verify</button>
    <button data-testid="cancel-step-up" @click="controller.onCancel()">Cancel</button>
  </div>`
} }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
const account = { id: 7, name: 'Offline test account', platform: 'openai', type: 'oauth' } as Account
const mountDialog = () => mount(OpenAISessionExportDialog, {
  props: { account },
  global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
})
let downloadedFilename: string | undefined
beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal('URL', { createObjectURL: vi.fn(() => 'blob:session'), revokeObjectURL: vi.fn() })
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) { downloadedFilename = this.download })
  downloadedFilename = undefined
})
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('OpenAISessionExportDialog', () => {
  it('requires a login Cookie and downloads the real upstream payload', async () => {
    const payload = { user: { id: 'test-user' }, accessToken: 'offline-test-token', expires: '2099-01-01T00:00:00Z', upstreamField: true }
    exportSession.mockResolvedValue(payload)
    const wrapper = mountDialog()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('input').setValue('__Secure-next-auth.session-token=test')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(exportSession).toHaveBeenCalledWith(7, '__Secure-next-auth.session-token=test')
    const blob = vi.mocked(URL.createObjectURL).mock.calls[0]?.[0] as Blob
    const content = await new Promise<string>(resolve => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result)); reader.readAsText(blob) })
    expect(JSON.parse(content)).toEqual(payload)
    expect(downloadedFilename).toBe('chatgpt-session-7.json')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('')
    wrapper.unmount()
  })
  it('shows the missing-Cookie error without downloading a synthetic Session', async () => {
    exportSession.mockRejectedValue({ reason: 'OPENAI_SESSION_COOKIE_REQUIRED' })
    const wrapper = mountDialog()
    await wrapper.get('input').setValue('other=value')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.accounts.sessionExport.OPENAI_SESSION_COOKIE_REQUIRED')
    expect(URL.createObjectURL).not.toHaveBeenCalled()
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('')
    wrapper.unmount()
  })
  it('discards a pending response after the dialog closes', async () => {
    let resolve!: (value: unknown) => void
    exportSession.mockReturnValue(new Promise(r => { resolve = r }))
    const wrapper = mountDialog()
    await wrapper.get('input').setValue('__Secure-next-auth.session-token=test')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('button[type="button"]').trigger('click')
    resolve({ accessToken: 'offline-test-token' }); await flushPromises()
    expect(URL.createObjectURL).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it.each(['close', 'switch account', 'unmount'] as const)('ignores late step-up errors after %s', async (action) => {
    let reject!: (reason: unknown) => void
    exportSession.mockReturnValue(new Promise((_, r) => { reject = r }))
    const wrapper = mountDialog()
    const controller = wrapper.findComponent({ name: 'TotpStepUpDialog' }).props('controller') as StepUpController
    await wrapper.get('input').setValue('__Secure-next-auth.session-token=test')
    await wrapper.get('form').trigger('submit')
    if (action === 'close') {
      await wrapper.get('button[type="button"]').trigger('click')
      await wrapper.setProps({ account: null })
    } else if (action === 'switch account') {
      await wrapper.setProps({ account: { ...account, id: 8 } })
    } else {
      wrapper.unmount()
    }
    reject({ reason: 'STEP_UP_REQUIRED' })
    await flushPromises()
    expect(controller.visible.value).toBe(false)
    expect(exportSession).toHaveBeenCalledTimes(1)
    expect(URL.createObjectURL).not.toHaveBeenCalled()
    if (action !== 'unmount') wrapper.unmount()
  })
  it('retries the same export after successful step-up verification', async () => {
    exportSession.mockRejectedValueOnce({ reason: 'STEP_UP_REQUIRED' })
      .mockResolvedValueOnce({ accessToken: 'offline-test-token' })
    const wrapper = mountDialog()
    await wrapper.get('input').setValue('__Secure-next-auth.session-token=test')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[data-testid="step-up"]').exists()).toBe(true)
    expect(URL.createObjectURL).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="verify"]').trigger('click')
    await flushPromises()
    expect(exportSession).toHaveBeenCalledTimes(2)
    expect(exportSession).toHaveBeenLastCalledWith(7, '__Secure-next-auth.session-token=test')
    expect(downloadedFilename).toBe('chatgpt-session-7.json')
    wrapper.unmount()
  })
  it('does not retry or download when step-up is cancelled', async () => {
    exportSession.mockRejectedValueOnce({ reason: 'STEP_UP_REQUIRED' })
    const wrapper = mountDialog()
    await wrapper.get('input').setValue('__Secure-next-auth.session-token=test')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    await wrapper.get('[data-testid="cancel-step-up"]').trigger('click')
    await flushPromises()
    expect(exportSession).toHaveBeenCalledTimes(1)
    expect(URL.createObjectURL).not.toHaveBeenCalled()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('')
    wrapper.unmount()
  })
})
