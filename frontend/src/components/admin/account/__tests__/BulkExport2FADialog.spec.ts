import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import BulkExport2FADialog from '../BulkExport2FADialog.vue'

const { exportTOTP, copyToClipboard } = vi.hoisted(() => ({ exportTOTP: vi.fn(), copyToClipboard: vi.fn() }))
vi.mock('@/api/admin/openaiTotp', () => ({ exportTOTP }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: object) => key + (params ? JSON.stringify(params) : '') }) }))

const dialog = {
  props: ['show'], emits: ['close'],
  template: '<div v-if="show"><slot /><slot name="footer" /><button data-testid="close" @click="$emit(\'close\')">close</button></div>'
}
const fixture = (id: number) => ({ email: `fixture${id}@example.test`, password: 'fixture-password', secret: `FIXTURE-SECRET-${id}` })
let wrapper: VueWrapper
function open(ids = [1, 2]) {
  wrapper = mount(BulkExport2FADialog, { props: { accountIds: ids }, global: { stubs: { BaseDialog: dialog } } })
  return wrapper
}
async function generate() {
  await wrapper.get('[data-testid="generate"]').trigger('click')
  await flushPromises()
}
function backup() { return (wrapper.get('[data-testid="backup-text"]').element as HTMLTextAreaElement).value }

beforeEach(() => {
  vi.resetAllMocks()
  exportTOTP.mockImplementation(async (id: number) => fixture(id))
})
afterEach(() => { wrapper?.unmount(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('bulk 2FA backup', () => {
  it('reads only explicitly selected IDs on demand and omits passwords by default', async () => {
    open([7, 205])
    expect(exportTOTP).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="copy"]').attributes('disabled')).toBeDefined()
    await generate()
    expect(exportTOTP.mock.calls.map(call => call[0])).toEqual([7, 205])
    expect(exportTOTP).toHaveBeenCalledWith(7, 'current', false, expect.any(AbortSignal))
    expect(backup()).toBe('fixture7@example.test----FIXTURE-SECRET-7\nfixture205@example.test----FIXTURE-SECRET-205\n')
    expect(backup()).not.toContain('fixture-password')
    await wrapper.get('[data-testid="copy"]').trigger('click')
    expect(copyToClipboard).toHaveBeenCalledWith(backup())
  })

  it('clears previous plaintext when changing password inclusion and regenerates three-field lines', async () => {
    open([1])
    await generate()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    expect(wrapper.find('[data-testid="backup-text"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="download"]').attributes('disabled')).toBeDefined()
    await generate()
    expect(exportTOTP).toHaveBeenLastCalledWith(1, 'current', true, expect.any(AbortSignal))
    expect(backup()).toBe('fixture1@example.test----fixture-password----FIXTURE-SECRET-1\n')
  })

  it('reports missing config and unresolved rotations without discarding successful accounts or exposing raw errors', async () => {
    exportTOTP.mockImplementation(async (id: number) => {
      if (id === 1) throw { reason: 'TOTP_CONFIG_REQUIRED', message: 'private-response-body' }
      if (id === 3) throw { reason: 'TOTP_UNRESOLVED' }
      return fixture(id)
    })
    open([1, 2, 3])
    await generate()
    expect(backup()).toBe('fixture2@example.test----FIXTURE-SECRET-2\n')
    expect(wrapper.text()).toContain('"success":1,"failed":2')
    expect(wrapper.text()).toContain('admin.accounts.bulkExport2fa.notConfigured')
    expect(wrapper.text()).toContain('admin.accounts.bulkExport2fa.unresolved')
    expect(wrapper.text()).not.toContain('private-response-body')
  })

  it('omits incomplete and ambiguous lines and never enables downloading an empty backup', async () => {
    exportTOTP.mockImplementation(async (id: number) => ({ ...fixture(id), password: id === 1 ? 'contains----delimiter' : 'contains\nnewline' }))
    open()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await generate()
    expect(wrapper.find('[data-testid="backup-text"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="download"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('"success":0,"failed":2')
    expect(wrapper.text()).toContain('admin.accounts.bulkExport2fa.invalidFields')
    exportTOTP.mockResolvedValue({ email: '', secret: '' })
    await generate()
    expect(wrapper.find('[data-testid="backup-text"]').exists()).toBe(false)
  })

  it('bounds concurrency, deduplicates IDs, and preserves selection order despite out-of-order responses', async () => {
    const resolve: Record<number, (value: ReturnType<typeof fixture>) => void> = {}
    exportTOTP.mockImplementation((id: number) => new Promise(done => { resolve[id] = done }))
    open([4, 2, 1, 3, 4])
    await wrapper.get('[data-testid="generate"]').trigger('click')
    expect(exportTOTP.mock.calls.map(call => call[0])).toEqual([4, 2, 1])
    resolve[2]!(fixture(2)); await flushPromises()
    expect(exportTOTP.mock.calls.map(call => call[0])).toEqual([4, 2, 1, 3])
    resolve[3]!(fixture(3)); resolve[1]!(fixture(1)); resolve[4]!(fixture(4))
    await flushPromises()
    expect(backup()).toBe([4, 2, 1, 3].map(id => `${fixture(id).email}----${fixture(id).secret}\n`).join(''))
  })

  it('cancels pending requests on close and ignores late secrets without fetching remaining accounts', async () => {
    const resolve: ((value: ReturnType<typeof fixture>) => void)[] = []
    exportTOTP.mockImplementation(() => new Promise(done => { resolve.push(done) }))
    open([1, 2, 3, 4])
    await wrapper.get('[data-testid="generate"]').trigger('click')
    const signals = exportTOTP.mock.calls.map(call => call[3] as AbortSignal)
    await wrapper.get('[data-testid="close"]').trigger('click')
    expect(signals.every(signal => signal.aborted)).toBe(true)
    resolve.forEach(done => done(fixture(1)))
    await flushPromises()
    expect(exportTOTP).toHaveBeenCalledTimes(3)
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(wrapper.find('[data-testid="backup-text"]').exists()).toBe(false)
    expect(wrapper.html()).not.toContain('FIXTURE-SECRET')
  })

  it('cancels requests on unmount', async () => {
    exportTOTP.mockImplementation(() => new Promise(() => {}))
    open([1])
    await wrapper.get('[data-testid="generate"]').trigger('click')
    const signal = exportTOTP.mock.calls[0]![3] as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
  })

  it('downloads the exact displayed content as UTF-8 TXT and revokes the object URL', async () => {
    const createObjectURL = vi.fn().mockReturnValue('blob:fixture')
    const revokeObjectURL = vi.fn()
    vi.stubGlobal('URL', { createObjectURL, revokeObjectURL })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    open([1])
    await generate()
    await wrapper.get('[data-testid="download"]').trigger('click')
    const blob = createObjectURL.mock.calls[0]![0] as Blob
    expect(blob.type).toBe('text/plain;charset=utf-8')
    const content = await new Promise<string>(resolve => {
      const reader = new FileReader()
      reader.onload = () => resolve(reader.result as string)
      reader.readAsText(blob)
    })
    expect(content).toBe(backup())
    const anchor = click.mock.instances[0] as HTMLAnchorElement
    expect(anchor.download).toMatch(/^openai-2fa-backup-.*\.txt$/)
    expect(anchor.href).toBe('blob:fixture')
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:fixture')
  })
})
