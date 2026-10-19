import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import OpenAISessionExportDialog from '../OpenAISessionExportDialog.vue'
import type { Account } from '@/types'

const { createTask, getTask, download } = vi.hoisted(() => ({ createTask: vi.fn(), getTask: vi.fn(), download: vi.fn() }))
vi.mock('@/api/admin/openaiTotp', () => ({ exportChatGPTSession: createTask, getChatGPTSessionExportTask: getTask, downloadChatGPTSession: download }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { name: 'TotpStepUpDialog', props: ['controller'], template: '<div />' } }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key, te: () => false }) }))
const account = { id: 7, name: 'Offline test account', platform: 'openai', type: 'oauth' } as Account
let filename: string | undefined
beforeEach(() => {
  vi.resetAllMocks(); vi.useFakeTimers()
  createTask.mockResolvedValue({ id: 9, account_id: 7, status: 'queued', stage: 'queued', attempt: 1 })
  getTask.mockResolvedValue({ id: 9, account_id: 7, status: 'succeeded', stage: 'succeeded', attempt: 1 })
  download.mockResolvedValue({ accessToken: 'token', sessionToken: 'session' })
  vi.stubGlobal('URL', { createObjectURL: vi.fn(() => 'blob:session'), revokeObjectURL: vi.fn() })
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) { filename = this.download })
  filename = undefined
})
afterEach(() => { vi.runOnlyPendingTimers(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals() })
const mountDialog = () => mount(OpenAISessionExportDialog, { props: { account }, global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } } })

describe('OpenAISessionExportDialog', () => {
  it('creates, polls, and downloads a successful task once', async () => {
    const wrapper = mountDialog(); await flushPromises(); vi.advanceTimersByTime(1000); await flushPromises()
    expect(createTask).toHaveBeenCalledWith(7); expect(getTask).toHaveBeenCalledWith(7, 9); expect(download).toHaveBeenCalledWith(7, 9)
    expect(filename).toBe('chatgpt-session-7.json'); expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })
  it('maps a worker failure and never downloads', async () => {
    createTask.mockResolvedValue({ id: 9, account_id: 7, status: 'queued', stage: 'queued', attempt: 1 })
    getTask.mockResolvedValue({ id: 9, account_id: 7, status: 'failed', stage: 'failed', error_code: 'totp_rejected', attempt: 1 })
    const wrapper = mountDialog(); await flushPromises(); vi.advanceTimersByTime(1000); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('tokenGuardV2.totpManage.sessionExport.failed'); expect(download).not.toHaveBeenCalled(); wrapper.unmount()
  })
  it('drops late responses after closing', async () => {
    let resolve!: (value: unknown) => void
    createTask.mockReturnValue(new Promise(r => { resolve = r }))
    const wrapper = mountDialog(); await wrapper.get('[data-testid="close"]').trigger('click')
    resolve({ id: 9, account_id: 7, status: 'succeeded', stage: 'succeeded', attempt: 1 }); await flushPromises()
    expect(download).not.toHaveBeenCalled(); wrapper.unmount()
  })
})
