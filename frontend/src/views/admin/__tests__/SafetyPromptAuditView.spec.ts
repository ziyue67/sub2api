import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SafetyPromptAuditView from '../SafetyPromptAuditView.vue'
import type { SafetyPromptConfig, SafetyPromptEvent, SafetyPromptStats } from '@/api/admin/safetyPrompt'

const mocks = vi.hoisted(() => ({
  getConfig: vi.fn(),
  updateConfig: vi.fn(),
  listEvents: vi.fn(),
}))

vi.mock('@/api/admin/safetyPrompt', () => ({
  getSafetyPromptConfig: mocks.getConfig,
  updateSafetyPromptConfig: mocks.updateConfig,
  listSafetyPromptEvents: mocks.listEvents,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const AppLayoutStub = { template: '<div><slot /></div>' }
const SecurityAuditNavStub = { props: ['active'], template: '<nav data-testid="security-nav" />' }

const stats: SafetyPromptStats = { persistent: true, count: 1, limit: 500 }
let config: SafetyPromptConfig
const events: SafetyPromptEvent[] = [{
  time: '2026-10-22T10:00:00Z',
  id: 'sp-events-1',
  request_id: 'req-1',
  model: 'gpt-5.1',
  upstream_account_id: 3,
  identity: 'unknown',
  action: 'suspected_policy_refusal',
  summary: 'hello, "world"',
  request_sha256: 'a'.repeat(64),
}]

function mountView() {
  return mount(SafetyPromptAuditView, {
    global: { stubs: { AppLayout: AppLayoutStub, SecurityAuditNav: SecurityAuditNavStub } },
  })
}

function readBlob(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error)
    reader.readAsText(blob)
  })
}

function findButton(wrapper: ReturnType<typeof mount>, label: string) {
  const button = wrapper.findAll('button').find(item => item.text() === label)
  if (!button) throw new Error(`button ${label} not found`)
  return button
}

describe('SafetyPromptAuditView', () => {
  beforeEach(() => {
    config = { enabled: true, signal_enabled: true, retention_days: 7 }
    mocks.getConfig.mockImplementation(async () => ({ config: { ...config }, stats }))
    mocks.updateConfig.mockImplementation(async (next: SafetyPromptConfig) => {
      config = { ...next }
      return { config: { ...config } }
    })
    mocks.listEvents.mockImplementation(async () => ({ events: events.map(item => ({ ...item })), stats }))
  })

  it('renders the native module switch, retention, and audit events', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="security-nav"]').exists()).toBe(true)
    expect((wrapper.get('[data-testid="safety-prompt-enabled"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid="safety-prompt-signal"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid="safety-prompt-retention"]').element as HTMLInputElement).value).toBe('7')
    expect(wrapper.get('[data-testid="safety-prompt-table"]').text()).toContain('sp-events-1')
    expect(wrapper.get('[data-testid="safety-prompt-table"]').text()).toContain('gpt-5.1')
    expect(wrapper.get('[data-testid="safety-prompt-table"]').text()).toContain('hello, "world"')
    expect(wrapper.get('[data-testid="safety-prompt-count"]').text()).toBe('admin.safetyPromptAudit.count')
    wrapper.unmount()
  })

  it('saves the module switch and retention values', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="safety-prompt-enabled"]').setValue(false)
    await wrapper.get('[data-testid="safety-prompt-retention"]').setValue('14')
    await findButton(wrapper, 'admin.safetyPromptAudit.save').trigger('click')
    await flushPromises()

    expect(mocks.updateConfig).toHaveBeenCalledWith({ enabled: false, signal_enabled: true, retention_days: 14 })
    expect(wrapper.text()).toContain('admin.safetyPromptAudit.saved')
    expect(wrapper.text()).toContain('admin.safetyPromptAudit.disabledNotice')
    wrapper.unmount()
  })

  it('filters by local date and rejects an inverted range', async () => {
    const wrapper = mountView()
    await flushPromises()
    const dateInputs = wrapper.findAll('input[type="date"]')
    await dateInputs[0].setValue('2026-10-23')
    await dateInputs[1].setValue('2026-10-22')
    expect(wrapper.text()).toContain('admin.safetyPromptAudit.badRange')
    expect(wrapper.find('[data-testid="safety-prompt-table"]').exists()).toBe(false)

    await dateInputs[0].setValue('2026-10-21')
    await dateInputs[1].setValue('2026-10-23')
    expect(wrapper.find('[data-testid="safety-prompt-table"]').exists()).toBe(true)
    await wrapper.get('input[type="search"]').setValue('no-match')
    expect(wrapper.find('[data-testid="safety-prompt-empty"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="safety-prompt-empty"]').text()).toBe('admin.safetyPromptAudit.empty')
    wrapper.unmount()
  })

  it('exports the filtered records as redacted JSON and CSV', async () => {
    const createObjectURL = vi.fn((_blob: Blob) => 'blob:test')
    const revokeObjectURL = vi.fn((_url: string) => {})
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURL })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

    const wrapper = mountView()
    await flushPromises()
    await findButton(wrapper, 'admin.safetyPromptAudit.exportJson').trigger('click')
    await findButton(wrapper, 'admin.safetyPromptAudit.exportCsv').trigger('click')
    await flushPromises()

    expect(createObjectURL).toHaveBeenCalledTimes(2)
    const jsonBlob = createObjectURL.mock.calls[0][0] as Blob
    const csvBlob = createObjectURL.mock.calls[1][0] as Blob
    const exported = JSON.parse(await readBlob(jsonBlob)) as { records: { id: string; summary: string }[] }
    expect(exported.records[0].id).toBe('sp-events-1')
    expect(exported.records[0].summary).toBe('hello, "world"')
    const csv = await readBlob(csvBlob)
    expect(csv).toContain('"hello, ""world"""')
    expect(csv).toContain('"sp-events-1"')
    click.mockRestore()
    wrapper.unmount()
  })
})
