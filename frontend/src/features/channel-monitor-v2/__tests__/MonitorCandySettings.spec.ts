import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import MonitorCandySettings from '../MonitorCandySettings.vue'
import MonitorSettingsPanel from '../MonitorSettingsPanel.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn(), groups: vi.fn(), error: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, te: () => false }) }))
vi.mock('@/utils/featureFlags', () => ({ isChannelMonitorV2Mode: () => true, getChannelMonitorMode: () => 'v2' }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.error, showSuccess: vi.fn() }) }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getAllIncludingInactive: mocks.groups } } }))
vi.mock('@/api/channelMonitorV2', () => ({ getConfig: mocks.get, updateConfig: mocks.update, MONITOR_ERROR_CATEGORIES: [] }))
const groups = [{ id: 7, name: 'Whole group', platform: 'openai' }, { id: 9, name: 'Another group', platform: 'anthropic' }]

it('creates a group probe with a configurable model, effort and interval, never an account ID', async () => {
  const wrapper = mount(MonitorCandySettings, { props: { modelValue: [], groups }, global: { stubs: { Toggle: true } } })
  await wrapper.get('[data-testid="candy-group-select"]').setValue('7')
  await wrapper.get('[data-testid="add-candy-probe"]').trigger('click')
  const value = wrapper.emitted('update:modelValue')![0][0] as any[]
  expect(value).toEqual([{ group_id: 7, model: '', enabled: true, reasoning_effort: 'medium', interval_minutes: 1 }])
  await wrapper.setProps({ modelValue: value })
  expect(wrapper.get('[data-testid="candy-group-select"]').text()).not.toContain('Whole group')
  expect(value[0]).not.toHaveProperty('account_id')
  await wrapper.find('input[maxlength="100"]').setValue('group-model')
  expect((wrapper.emitted('update:modelValue')!.at(-1)![0] as any[])[0].model).toBe('group-model')
  wrapper.unmount()
})

it('persists candy configuration through the existing versioned V2 admin save', async () => {
  const config = { version: 3, enabled: true, refresh_interval_seconds: 60, platforms: [], group_ids: [], candy_probes: [], ignored_error_categories: [] }
  mocks.get.mockResolvedValue(config)
  mocks.groups.mockResolvedValue(groups)
  mocks.update.mockImplementation(async payload => ({ ...payload, version: 4 }))
  const wrapper = mount(MonitorSettingsPanel, { global: { stubs: { Toggle: true, Icon: true, RouterLink: true } } })
  await flushPromises()
  const probe = { group_id: 7, enabled: true, model: 'group-model', reasoning_effort: 'medium', interval_minutes: 1 }
  wrapper.findComponent(MonitorCandySettings).vm.$emit('update:modelValue', [probe])
  await flushPromises()
  await wrapper.get('header button.btn-primary').trigger('click')
  await flushPromises()
  expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({ version: 3, candy_probes: [probe] }))
  wrapper.findComponent(MonitorCandySettings).vm.$emit('update:modelValue', [{ ...probe, interval_minutes: 1.5 }])
  await flushPromises()
  await wrapper.get('header button.btn-primary').trigger('click')
  expect(mocks.update).toHaveBeenCalledTimes(1)
  expect(mocks.error).toHaveBeenCalledWith('channelMonitorV2.candy.invalid')
  wrapper.unmount()
})
