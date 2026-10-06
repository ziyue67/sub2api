import { defineComponent, reactive } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ChannelMonitorView from '@/views/admin/ChannelMonitorView.vue'

const mocks = vi.hoisted(() => ({ setMonitorMode: vi.fn(), fetchPublicSettings: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
const store = vi.hoisted(() => ({ settings: null as null | { channel_monitor_enabled: boolean; channel_monitor_mode: string } }))

vi.mock('@/utils/featureFlags', () => ({
  getChannelMonitorMode: () => store.settings!.channel_monitor_mode,
  isChannelMonitorV1Mode: () => store.settings!.channel_monitor_mode === 'v1',
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    get cachedPublicSettings() { return store.settings },
    fetchPublicSettings: mocks.fetchPublicSettings,
    showSuccess: mocks.showSuccess,
    showError: mocks.showError,
  }),
}))
vi.mock('@/api/channelMonitorV3', () => ({ setMonitorMode: mocks.setMonitorMode }))
vi.mock('@/api/admin', () => ({ adminAPI: { channelMonitor: { list: vi.fn().mockResolvedValue({ items: [], total: 0 }) } } }))
vi.mock('@/features/channel-monitor-v2/MonitorSettingsPanel.vue', () => ({ default: { template: '<div data-testid="v2-panel" />' } }))
vi.mock('@/features/channel-monitor-v3/V3SettingsPanel.vue', () => ({ default: { template: '<div data-testid="v3-panel" />' } }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key) }),
  }
})

const ConfirmStub = defineComponent({
  props: { show: Boolean, message: { type: String, default: '' } },
  emits: ['confirm', 'cancel'],
  template: '<div v-if="show" data-testid="confirm"><span>{{ message }}</span><button type="button" data-testid="confirm-yes" @click="$emit(\'confirm\')" /><button type="button" data-testid="confirm-no" @click="$emit(\'cancel\')" /></div>',
})
const stubs = {
  AppLayout: { template: '<main><slot /></main>' },
  TablePageLayout: true, ConfirmDialog: ConfirmStub, MonitorFormDialog: true, MonitorTemplateManagerDialog: true,
  MonitorRunResultDialog: true, Icon: true, RouterLink: true,
}

beforeEach(() => {
  vi.clearAllMocks()
  store.settings = reactive({ channel_monitor_enabled: true, channel_monitor_mode: 'v2' })
  mocks.setMonitorMode.mockImplementation(async (mode: string) => mode)
  mocks.fetchPublicSettings.mockImplementation(async () => {
    store.settings!.channel_monitor_mode = mocks.setMonitorMode.mock.calls.at(-1)?.[0] ?? store.settings!.channel_monitor_mode
    return store.settings
  })
})

describe('channel monitor site mode switch', () => {
  it('opens the tab of the current mode and switches the site after confirmation', async () => {
    const wrapper = mount(ChannelMonitorView, { global: { stubs } })
    await flushPromises()
    expect(wrapper.find('[data-testid="v2-panel"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="monitor-mode-v2"]').attributes('aria-checked')).toBe('true')

    await wrapper.get('[data-testid="monitor-mode-v3"]').trigger('click')
    expect(wrapper.get('[data-testid="confirm"]').text()).toContain('channelMonitorV3.admin.switchEffect.v3')
    expect(mocks.setMonitorMode).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="confirm-yes"]').trigger('click')
    await flushPromises()

    expect(mocks.setMonitorMode).toHaveBeenCalledWith('v3')
    expect(mocks.fetchPublicSettings).toHaveBeenCalledWith(true)
    expect(wrapper.find('[data-testid="v3-panel"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="monitor-mode-v3"]').attributes('aria-checked')).toBe('true')
    expect(mocks.showSuccess).toHaveBeenCalled()
  })

  it('does nothing when the switch is cancelled or the mode is unchanged', async () => {
    const wrapper = mount(ChannelMonitorView, { global: { stubs } })
    await flushPromises()
    await wrapper.get('[data-testid="monitor-mode-v1"]').trigger('click')
    await wrapper.get('[data-testid="confirm-no"]').trigger('click')
    await wrapper.get('[data-testid="monitor-mode-v2"]').trigger('click')
    await wrapper.get('[data-testid="confirm-yes"]').trigger('click')
    await flushPromises()
    expect(mocks.setMonitorMode).not.toHaveBeenCalled()
  })

  it('keeps the switch disabled while the feature is off', async () => {
    store.settings!.channel_monitor_enabled = false
    const wrapper = mount(ChannelMonitorView, { global: { stubs } })
    await flushPromises()
    expect(wrapper.get('[data-testid="monitor-mode-v3"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('channelMonitorV3.admin.siteModeOff')
  })
})
