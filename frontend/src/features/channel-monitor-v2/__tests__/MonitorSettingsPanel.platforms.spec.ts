import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { beforeEach, expect, it, vi } from 'vitest'
import MonitorSettingsPanel from '../MonitorSettingsPanel.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn(), groups: vi.fn(), error: vi.fn() }))
vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return { ...actual, useI18n: () => ({ t: (key: string) => key, te: () => false }) }
})
vi.mock('@/utils/featureFlags', () => ({ isChannelMonitorV2Mode: () => true, getChannelMonitorMode: () => 'v2' }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.error, showSuccess: vi.fn(), cachedPublicSettings: null }) }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getAllIncludingInactive: mocks.groups } } }))
vi.mock('@/api/channelMonitorV2', () => ({ getConfig: mocks.get, updateConfig: mocks.update, MONITOR_ERROR_CATEGORIES: ['timeout'] }))

const factory = ['anthropic', 'openai', 'grok', 'kiro', 'gemini', 'antigravity']

function config() {
  return {
    version: 3,
    enabled: true,
    refresh_interval_seconds: 60,
    platforms: factory.map((platform) => ({ platform, enabled: true, models: [] })),
    group_ids: [],
    candy_probes: [],
    candy_history_limit: 100,
    ignored_error_categories: [],
    health_thresholds: {},
  }
}

beforeEach(() => {
  mocks.get.mockReset()
  mocks.update.mockReset()
  mocks.groups.mockReset()
  mocks.error.mockReset()
})

it('adds supported and custom platforms, reorders them, and deletes only a disabled custom platform', async () => {
  mocks.get.mockResolvedValue(config())
  mocks.groups.mockResolvedValue([
    { id: 9, name: 'DS', platform: 'deepseek' },
    { id: 11, name: 'Legacy', platform: 'custom_gw' },
  ])
  mocks.update.mockImplementation(async (payload) => ({ ...payload, version: 4 }))
  const wrapper = mount(MonitorSettingsPanel, { global: { stubs: { Toggle: true, Icon: true, RouterLink: true } } })
  await flushPromises()

  const optionValues = wrapper.get('[data-testid="monitor-v2-platform-select"]').findAll('option').map((option) => option.element.value)
  expect(optionValues).toEqual(expect.arrayContaining(['deepseek', 'kimi', 'typesafe', 'command_code', 'cline', 'custom_gw', 'composite']))
  expect(optionValues).not.toContain('openai')

  await wrapper.get('[data-testid="monitor-v2-platform-select"]').setValue('deepseek')
  await wrapper.get('[data-testid="monitor-v2-add-platform"]').trigger('click')
  await wrapper.get('[data-testid="monitor-v2-platform-custom"]').setValue('Custom_GW')
  await wrapper.get('[data-testid="monitor-v2-add-platform"]').trigger('click')
  await wrapper.get('[data-testid="monitor-v2-platform-custom"]').setValue('not a platform')
  await wrapper.get('[data-testid="monitor-v2-add-platform"]').trigger('click')
  expect(mocks.error).toHaveBeenCalledWith('channelMonitorV2.settings.platformInvalid')

  const rows = () => wrapper.findAll('[data-testid="monitor-v2-platform-row"]').map((row) => row.attributes('data-platform'))
  expect(rows().slice(-2)).toEqual(['deepseek', 'custom_gw'])
  expect(wrapper.find('[data-platform="openai"]').find('[data-testid="monitor-v2-delete-platform"]').exists()).toBe(false)
  expect(wrapper.get('[data-platform="openai"]').element.lastElementChild?.querySelector('[data-testid="monitor-v2-move-up"]')).toBeTruthy()
  expect(wrapper.get('[data-platform="custom_gw"]').get('[data-testid="monitor-v2-delete-platform"]').attributes('disabled')).toBeDefined()

  await wrapper.get('[data-platform="custom_gw"]').get('[data-testid="monitor-v2-move-up"]').trigger('click')
  expect(rows().slice(-2)).toEqual(['custom_gw', 'deepseek'])

  const draft = (wrapper.vm as unknown as { draft: { platforms: Array<{ platform: string; enabled: boolean }> } }).draft
  const custom = draft.platforms.find((item) => item.platform === 'custom_gw')
  expect(custom).toBeTruthy()
  custom!.enabled = false
  await nextTick()
  await wrapper.get('[data-platform="custom_gw"]').get('[data-testid="monitor-v2-delete-platform"]').trigger('click')
  expect(rows()).not.toContain('custom_gw')
  expect(rows()).toContain('deepseek')

  await wrapper.get('header button.btn-primary').trigger('click')
  await flushPromises()
  const saved = mocks.update.mock.calls[0][0].platforms.map((item: { platform: string }) => item.platform)
  expect(saved.slice(0, 6)).toEqual(factory)
  expect(saved.at(-1)).toBe('deepseek')
  expect(saved).not.toContain('custom_gw')
  expect(mocks.update.mock.calls[0][0].card_timeline_mode).toBe('windows')
  wrapper.unmount()
})

it('moves groups inside one platform and saves that display order', async () => {
  mocks.get.mockResolvedValue(config())
  mocks.groups.mockResolvedValue([
    { id: 3, name: 'Zed', platform: 'openai' },
    { id: 8, name: 'Alpha', platform: 'openai' },
    { id: 1, name: 'A', platform: 'anthropic' },
  ])
  mocks.update.mockImplementation(async (payload) => ({ ...payload, version: 4 }))
  const wrapper = mount(MonitorSettingsPanel, { global: { stubs: { Toggle: true, Icon: true, RouterLink: true } } })
  await flushPromises()
  const section = (platform: string) => wrapper.get(`[data-testid="monitor-v2-group-platform"][data-platform="${platform}"]`)
  const ids = () => section('openai').findAll('[data-testid="monitor-v2-group-row"]').map((row) => row.attributes('data-group-id'))
  expect(ids()).toEqual(['8', '3'])
  expect(section('openai').get('[data-testid="monitor-v2-group-row"]').element.lastElementChild?.querySelector('[data-testid="monitor-v2-group-move-up"]')).toBeTruthy()
  await section('openai').findAll('[data-testid="monitor-v2-group-move-down"]')[0].trigger('click')
  expect(ids()).toEqual(['3', '8'])
  expect(section('anthropic').get('[data-testid="monitor-v2-group-row"]').attributes('data-group-id')).toBe('1')
  await wrapper.get('header button.btn-primary').trigger('click')
  await flushPromises()
  expect(mocks.update.mock.calls[0][0].group_order).toEqual([1, 3, 8])
  wrapper.unmount()
})

it('saves the admin card timeline mode and keeps an omitted value on time windows', async () => {
  mocks.get.mockResolvedValue({ ...config(), card_timeline_mode: 'samples' })
  mocks.groups.mockResolvedValue([])
  mocks.update.mockImplementation(async (payload) => ({ ...payload, version: 4 }))
  const wrapper = mount(MonitorSettingsPanel, { global: { stubs: { Toggle: true, Icon: true, RouterLink: true } } })
  await flushPromises()
  expect(wrapper.get('[data-testid="monitor-v2-card-timeline-samples"]').classes()).toContain('tab-active')
  await wrapper.get('[data-testid="monitor-v2-card-timeline-windows"]').trigger('click')
  await wrapper.get('header button.btn-primary').trigger('click')
  await flushPromises()
  expect(mocks.update.mock.calls[0][0].card_timeline_mode).toBe('windows')
  wrapper.unmount()
})
