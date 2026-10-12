import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import IdleProbeSettings from '../IdleProbeSettings.vue'
import type { MonitorV3Component, MonitorV3IdleProbe, MonitorV3IdleProbeStat } from '@/api/channelMonitorV3'

const mocks = vi.hoisted(() => ({ updateIdleProbe: vi.fn(), error: vi.fn(), success: vi.fn() }))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key),
    te: (key: string) => key.startsWith('channelMonitorV3.errors.'),
  }),
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.error, showSuccess: mocks.success }) }))
vi.mock('@/api/channelMonitorV3', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/channelMonitorV3')>()),
  updateIdleProbe: mocks.updateIdleProbe,
}))

const ToggleStub = {
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: '<button type="button" :data-on="String(modelValue)" @click="$emit(\'update:modelValue\', !modelValue)" />',
}

function component(id: number, groupId: number, extra: Partial<MonitorV3Component> = {}): MonitorV3Component {
  return {
    id, category_id: 1, name: `Component ${id}`, description: '', group_id: groupId, model: '', degraded_ttft_ms: 0,
    show_multiplier: true, visibility: 'group', enabled: true, sort_order: id, group_name: `Group ${groupId}`, group_platform: 'openai',
    group_status: 'active', group_rate_multiplier: 1, group_deleted: false, created_at: '', updated_at: '', ...extra,
  }
}

const components = [
  component(1, 7),
  component(2, 7, { model: 'gpt-5.5-mini' }),
  component(3, 8, { model: 'claude-opus-5-5', group_platform: 'anthropic' }),
  component(4, 8, { model: 'claude-sonnet-5-5', group_platform: 'anthropic' }),
  component(5, 9, { group_status: 'disabled' }),
]

function render(probe: MonitorV3IdleProbe, stats: MonitorV3IdleProbeStat[] = []) {
  return mount(IdleProbeSettings, { props: { probe, stats, components }, global: { stubs: { Toggle: ToggleStub } } })
}
const rows = (wrapper: ReturnType<typeof render>) => wrapper.findAll('[data-testid="monitor-v3-idle-probe-group"]')

beforeEach(() => {
  vi.clearAllMocks()
  mocks.updateIdleProbe.mockImplementation(async (probe: MonitorV3IdleProbe) => probe)
})

describe('idle probe settings', () => {
  it('lists each group the components use once', () => {
    const wrapper = render({ enabled: false, groups: [] })
    expect(rows(wrapper).map((row) => row.attributes('data-group'))).toEqual(['7', '8', '9'])
    expect(rows(wrapper)[0].text()).toContain('channelMonitorV3.admin.idleProbe.componentCount:{"count":2}')
    expect(rows(wrapper)[2].text()).toContain('channelMonitorV3.admin.idleProbe.groupUnavailable')
    expect(rows(wrapper)[2].get('[data-testid="monitor-v3-idle-probe-check"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="monitor-v3-idle-probe-model"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="monitor-v3-idle-probe-save"]').attributes('disabled')).toBeDefined()
  })

  it('needs a model for whole-group components and saves the checked groups', async () => {
    const wrapper = render({ enabled: false, groups: [] })
    await wrapper.get('[data-testid="monitor-v3-idle-probe-enabled"]').trigger('click')
    await rows(wrapper)[0].get('[data-testid="monitor-v3-idle-probe-check"]').setValue(true)
    await rows(wrapper)[1].get('[data-testid="monitor-v3-idle-probe-check"]').setValue(true)
    expect(rows(wrapper)[0].text()).toContain('channelMonitorV3.admin.idleProbe.modelRequired')
    expect(rows(wrapper)[1].text()).toContain('channelMonitorV3.admin.idleProbe.componentModels:{"models":"claude-opus-5-5、claude-sonnet-5-5"}')
    expect(rows(wrapper)[1].find('[data-testid="monitor-v3-idle-probe-model"]').exists()).toBe(false)
    const save = wrapper.get('[data-testid="monitor-v3-idle-probe-save"]')
    expect(save.attributes('disabled')).toBeDefined()

    await rows(wrapper)[0].get('[data-testid="monitor-v3-idle-probe-model"]').setValue(' gpt-5.5 ')
    await rows(wrapper)[1].get('[data-testid="monitor-v3-idle-probe-effort"]').setValue('')
    expect(save.attributes('disabled')).toBeUndefined()
    await save.trigger('click')
    await flushPromises()
    const sent = {
      enabled: true,
      groups: [{ group_id: 7, model: 'gpt-5.5', reasoning_effort: 'low' }, { group_id: 8, model: '', reasoning_effort: '' }],
    }
    expect(mocks.updateIdleProbe).toHaveBeenCalledWith(sent)
    expect(wrapper.emitted('saved')).toEqual([[sent]])
    expect(mocks.success).toHaveBeenCalledWith('channelMonitorV3.admin.idleProbe.saved')
  })

  it('starts from the saved settings and shows the latest results', () => {
    const wrapper = render(
      { enabled: true, groups: [{ group_id: 7, model: 'gpt-5.5', reasoning_effort: 'medium' }, { group_id: 8, model: '', reasoning_effort: '' }] },
      [
        { group_id: 7, probes: 12, failures: 1, cost_usd: 0.0042, last_at: '2026-10-11T09:34:30Z', last_model: 'gpt-5.5', last_outcome: 'success', last_latency_ms: 1834 },
        { group_id: 8, probes: 3, failures: 3, cost_usd: 0, last_at: '2026-10-11T09:34:30Z', last_model: 'claude-opus-5-5', last_outcome: 'failure', last_category: 'not_found', last_error: 'API returned 404: no such model' },
      ],
    )
    expect(wrapper.get('[data-testid="monitor-v3-idle-probe-enabled"]').attributes('data-on')).toBe('true')
    expect((rows(wrapper)[0].get('[data-testid="monitor-v3-idle-probe-model"]').element as HTMLInputElement).value).toBe('gpt-5.5')
    expect((rows(wrapper)[0].get('[data-testid="monitor-v3-idle-probe-effort"]').element as HTMLSelectElement).value).toBe('medium')
    const first = rows(wrapper)[0].get('[data-testid="monitor-v3-idle-probe-stats"]').text()
    expect(first).toContain('channelMonitorV3.admin.idleProbe.lastSuccess:{\\"seconds\\":\\"1.8\\"}')
    expect(first).toContain('"probes":12,"failures":1,"cost":"0.0042"')
    const second = rows(wrapper)[1].get('[data-testid="monitor-v3-idle-probe-stats"]').text()
    expect(second).toContain('channelMonitorV3.admin.idleProbe.lastFailure:{\\"reason\\":\\"channelMonitorV3.errors.not_found\\"}')
    expect(rows(wrapper)[1].get('[data-testid="monitor-v3-idle-probe-issue"]').text()).toContain('API returned 404: no such model')
    expect(rows(wrapper)[2].text()).toContain('channelMonitorV3.admin.idleProbe.noProbes')
    expect(wrapper.get('[data-testid="monitor-v3-idle-probe-save"]').attributes('disabled')).toBeDefined()
  })

  it('keeps the edits when saving fails', async () => {
    mocks.updateIdleProbe.mockRejectedValue(new Error('boom'))
    const wrapper = render({ enabled: true, groups: [{ group_id: 8, model: '', reasoning_effort: 'low' }] })
    await rows(wrapper)[1].get('[data-testid="monitor-v3-idle-probe-effort"]').setValue('high')
    await wrapper.get('[data-testid="monitor-v3-idle-probe-save"]').trigger('click')
    await flushPromises()
    expect(mocks.error).toHaveBeenCalled()
    expect(wrapper.emitted('saved')).toBeUndefined()
    expect((rows(wrapper)[1].get('[data-testid="monitor-v3-idle-probe-effort"]').element as HTMLSelectElement).value).toBe('high')
  })

  it('shows an empty state without components', () => {
    const wrapper = mount(IdleProbeSettings, { props: { probe: { enabled: false, groups: [] }, stats: [], components: [] }, global: { stubs: { Toggle: ToggleStub } } })
    expect(wrapper.find('[data-testid="monitor-v3-idle-probe-empty"]').exists()).toBe(true)
  })
})
