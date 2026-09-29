import { beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import AutoConfigView from '../AutoConfigView.vue'
import { getAutoConfig, saveAutoConfig, type AutoConfig } from '@/api/admin/autoConfig'
import { getAll } from '@/api/admin/groups'
import type { Group } from '@/types'
import { defaultExcelBPSDefaults } from '@/utils/excelBPSDefaults'
vi.mock('@/components/account/ModelWhitelistSelector.vue', () => ({ default: { props: ['modelValue'], template: '<div data-testid="model-selection">{{ modelValue.join(", ") }}</div>' } }))
const state = vi.hoisted(() => ({ auth: null as any }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => state.auth }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/components/admin/operations/AutoConfigHistory.vue', () => ({ default: { props: ['refreshKey'], template: '<section data-testid="history" :data-refresh="refreshKey" />' } }))
vi.mock('@/components/admin/operations/SmartOpsNav.vue', () => ({ default: { template: '<nav />' } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/autoConfig', () => ({ getAutoConfig: vi.fn(), saveAutoConfig: vi.fn() }))
vi.mock('@/api/admin/groups', () => ({ getAll: vi.fn() }))
const config: AutoConfig = { excel_bps: defaultExcelBPSDefaults(), enabled: false, platform: 'openai', priority: 50, load_factor: 1, concurrency: 3, group_ids: [], upgrade_enabled: false, upgrade_group_ids: [], successes_per_step: 20, upgrade_step: 1, max_concurrency: 100, cooldown_seconds: 60, revision: '' }
beforeEach(() => {
 vi.resetAllMocks(); state.auth = reactive({ user: { id: 1, role: 'admin' } })
 vi.mocked(getAutoConfig).mockResolvedValue(structuredClone(config)); vi.mocked(saveAutoConfig).mockImplementation(async c => c)
 vi.mocked(getAll).mockResolvedValue([{ id: 5, name: 'OpenAI test', platform: 'openai', status: 'active' }, { id: 6, name: 'Anthropic test', platform: 'anthropic', status: 'active' }] as Group[])
})
describe('AutoConfigView', () => {
 it('saves only the reusable BPS template without any account activation switch', async () => {
  const w = mount(AutoConfigView); await flushPromises()
  expect(w.find('[data-testid="bps-initialize_enabled"]').exists()).toBe(false)
  expect(w.get<HTMLInputElement>('[data-testid="bps-ignore_encrypted_content"]').element.checked).toBe(true)
  await w.get('[data-testid="bps-ignore_encrypted_content"]').setValue(false)
  await w.get('form').trigger('submit'); await flushPromises()
  expect(saveAutoConfig).toHaveBeenCalledWith({ ...config, excel_bps: { ...config.excel_bps, ignore_encrypted_content: false } }); w.unmount()
 })
 it('restores the recommended template options', async () => {
  const w = mount(AutoConfigView); await flushPromises()
  await w.get('[data-testid="bps-ignore_encrypted_content"]').setValue(false)
  await w.get('[data-testid="bps-reset-options"]').trigger('click')
  expect(w.get<HTMLInputElement>('[data-testid="bps-ignore_encrypted_content"]').element.checked).toBe(true); w.unmount()
 })
 it('keeps recovery dependent on automatic 403 disabling', async () => {
  const w = mount(AutoConfigView); await flushPromises()
  await w.get('[data-testid="bps-auto_recover_on_403"]').setValue(true)
  await w.get('[data-testid="bps-auto_disable_on_403"]').setValue(false)
  const recovery = w.get<HTMLInputElement>('[data-testid="bps-auto_recover_on_403"]')
  expect(recovery.element.disabled).toBe(true); expect(recovery.element.checked).toBe(false); w.unmount()
 })
 it('fills compatible defaults when loading legacy settings', async () => {
  const { excel_bps: _bps, ...legacy } = config
  vi.mocked(getAutoConfig).mockResolvedValueOnce(legacy)
  const w = mount(AutoConfigView); await flushPromises()
  await w.get('form').trigger('submit'); await flushPromises()
  expect(saveAutoConfig).toHaveBeenCalledWith(config); w.unmount()
 })

 it('saves initial OAuth values without enabling upgrades', async () => {
  const w = mount(AutoConfigView); await flushPromises()
  await w.get('[data-testid="initial-enabled"]').setValue(true); await w.get('[data-testid="initial-group-5"]').setValue(true)
  await w.get('[data-testid="priority"]').setValue(0); await w.get('[data-testid="load_factor"]').setValue(90); await w.get('[data-testid="concurrency"]').setValue(7)
  expect(w.find('[data-testid="initial-group-6"]').exists()).toBe(false)
  await w.get('form').trigger('submit'); await flushPromises()
  expect(w.get('[data-testid="history"]').attributes('data-refresh')).toBe('1')
  expect(saveAutoConfig).toHaveBeenCalledWith({ ...config, enabled: true, priority: 0, load_factor: 90, concurrency: 7, group_ids: [5] }); w.unmount()
 })
 it('enables upgrades independently and validates scope', async () => {
  const w = mount(AutoConfigView); await flushPromises(); await w.get('[data-testid="upgrade-enabled"]').setValue(true)
  await w.get('form').trigger('submit'); expect(saveAutoConfig).not.toHaveBeenCalled()
  await w.get('[data-testid="upgrade-group-6"]').setValue(true); await w.get('form').trigger('submit'); await flushPromises()
  expect(saveAutoConfig).toHaveBeenCalledWith({ ...config, upgrade_enabled: true, upgrade_group_ids: [6] }); w.unmount()
 })
 it('clears incompatible initial groups when platform changes', async () => {
  const w = mount(AutoConfigView); await flushPromises(); await w.get('[data-testid="initial-group-5"]').setValue(true)
  await w.get('[data-testid="platform"]').setValue('anthropic'); await w.get('form').trigger('submit'); await flushPromises()
  expect(saveAutoConfig).toHaveBeenCalledWith({ ...config, platform: 'anthropic' }); w.unmount()
 })
 it('does not allow saving before a successful load and supports retry', async () => {
  vi.mocked(getAutoConfig).mockRejectedValueOnce(new Error('offline')); const w = mount(AutoConfigView); await flushPromises()
  expect(w.find('form').exists()).toBe(false); await w.get('button').trigger('click'); await flushPromises(); expect(w.find('form').exists()).toBe(true); w.unmount()
 })
 it('preserves draft after a save error', async () => {
  const w = mount(AutoConfigView); await flushPromises(); vi.mocked(saveAutoConfig).mockRejectedValueOnce(new Error('offline'))
  await w.get('[data-testid="concurrency"]').setValue(12); await w.get('form').trigger('submit'); await flushPromises()
  expect(w.get('[data-testid="history"]').attributes('data-refresh')).toBe('0')
  expect((w.get('[data-testid="concurrency"]').element as HTMLInputElement).value).toBe('12'); expect(w.get('[role="alert"]').text()).toContain('saveFailed'); w.unmount()
 })
 it('discards a pending settings response after logout', async () => {
  let resolve!: (v: AutoConfig) => void; vi.mocked(getAutoConfig).mockReturnValueOnce(new Promise(r => { resolve = r }))
  const w = mount(AutoConfigView); state.auth.user = null; resolve(config); await flushPromises(); expect(w.find('form').exists()).toBe(false); w.unmount()
 })
})
