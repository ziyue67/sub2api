import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { readFileSync } from 'node:fs'
import { auditTab, listIPAudit, releaseIPAudit, type AuditEntry, type AuditPage } from '../api'
import IPAuditPanel from '../IPAuditPanel.vue'
import en from '@/i18n/locales/en/ipAudit'
import zh from '@/i18n/locales/zh/ipAudit'
vi.mock('../api', async () => ({ ...(await vi.importActual('../api')), listIPAudit: vi.fn(), releaseIPAudit: vi.fn() }))
const entry: AuditEntry = { id: 'stable', source: 'anonymous_api', network: '8.8.8.8/32', scope: 'invalid_credentials', reason: 'anonymous_api_unknown', state: 'active', effective: 'conditional', manual: false, can_release: true, started_at: 100, expires_at: 200, count: null, threshold: 30, version: 'version' }
const page = (): AuditPage => ({ items: [entry], sources: [
  { id: 'anonymous_api', scope: 'invalid_credentials', storage: 'redis', enumeration: 'indexed', can_release: true },
  { id: 'login_failures', scope: 'authentication', storage: 'redis', enumeration: 'exact_ip', can_release: true },
  { id: 'manual_deny', scope: 'site', storage: 'settings', enumeration: 'bounded_config', can_release: false, manage_url: '/admin/settings?tab=security#ip-risk-settings' },
  { id: 'host_firewall', scope: 'external_host', storage: 'not_connected', enumeration: 'not_connected', can_release: false }
], has_more: true, next_cursor: 20, unrestricted: false })
function render(locale: 'en' | 'zh' = 'en') {
 return mount(IPAuditPanel, { global: { plugins: [createI18n({ legacy: false, locale, messages: { en: { ipAudit: en, common: { search: 'Search', refresh: 'Refresh', cancel: 'Cancel', actions: 'Actions', loading: 'Loading' } }, zh: { ipAudit: zh, common: { search: '搜索', refresh: '刷新', cancel: '取消', actions: '操作', loading: '加载中' } } } })] } })
}
beforeEach(() => {
 vi.resetAllMocks();vi.mocked(listIPAudit).mockResolvedValue(page())
 vi.mocked(releaseIPAudit).mockResolvedValue({ released: true, readback: 'released', remaining: [{ ...entry, source: 'manual_deny', manual: true, can_release: false }], unknown_sources: ['host_firewall'], unrestricted: false })
})
describe('IP audit', () => {
 it.each(['en', 'zh'] as const)('uses real %s labels and external-source boundaries', async (locale) => {
  const wrapper = render(locale);await flushPromises()
  expect(wrapper.text()).toContain(locale === 'en' ? en.externalHint : zh.externalHint)
  expect(wrapper.text()).not.toContain('ipAudit.')
  expect(wrapper.find('[data-testid="ip-open-settings"]').exists()).toBe(true)
  wrapper.unmount()
 })
 it('sends filters and bounded page cursors to the server, and clears selection on refresh', async () => {
  const wrapper = render();await flushPromises()
  await wrapper.get('[data-testid="audit-ip"]').setValue(' 8.8.8.8 ')
  await wrapper.get('[data-testid="audit-state"]').setValue('active')
  await wrapper.get('form').trigger('submit');await flushPromises()
  expect(listIPAudit).toHaveBeenLastCalledWith(expect.objectContaining({ source: 'anonymous_api', ip: '8.8.8.8', state: 'active', cursor: 0, limit: 20 }), expect.anything())
  await wrapper.get('[data-testid="audit-next"]').trigger('click');await flushPromises()
  expect(listIPAudit).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: 20 }), expect.anything())
  wrapper.unmount()
 })
 it('requires per-source confirmation, then displays readback and remaining restrictions', async () => {
  const wrapper = render();await flushPromises()
  await wrapper.get('[data-testid="release-source"]').trigger('click')
  expect(releaseIPAudit).not.toHaveBeenCalled()
  expect(wrapper.get('[data-testid="release-dialog"]').text()).toContain(entry.network)
  await wrapper.get('[data-testid="release-confirm"]').trigger('click');await flushPromises()
  expect(releaseIPAudit).toHaveBeenCalledWith(entry, 'operator_review')
  expect(listIPAudit).toHaveBeenCalledTimes(2)
  const result = wrapper.get('[data-testid="release-outcome"]').text()
  expect(result).toContain(en.released);expect(result).toContain(en.notUnrestricted)
  expect(result).toContain(en.sources.manual_deny);expect(result).toContain(en.sources.host_firewall)
  wrapper.unmount()
 })
 it('requires a distinct confirmation for the explicitly user-wide penalty action', async () => {
  const userEntry = { ...entry, source: 'security_penalty', network: '', user_id: 9, scope: 'all_user_states', version: 'explicit-user-scope-v1' }
  vi.mocked(listIPAudit).mockResolvedValue({ ...page(), items: [userEntry] })
  const wrapper = render();await flushPromises()
  await wrapper.get('[data-testid="release-source"]').trigger('click')
  expect(wrapper.get('[data-testid="release-dialog"]').text()).toContain(en.confirmUserScope)
  expect(wrapper.get('[data-testid="release-dialog"]').text()).toContain('UID 9')
  expect(releaseIPAudit).not.toHaveBeenCalled()
  await wrapper.get('[data-testid="release-confirm"]').trigger('click');await flushPromises()
  expect(releaseIPAudit).toHaveBeenCalledWith(userEntry, 'operator_review')
  wrapper.unmount()
 })
 it('reports failed release and re-reads state without claiming success', async () => {
  vi.mocked(releaseIPAudit).mockRejectedValue(new Error('conflict'))
  const wrapper = render();await flushPromises()
  await wrapper.get('[data-testid="release-source"]').trigger('click');await wrapper.get('[data-testid="release-confirm"]').trigger('click');await flushPromises()
  expect(wrapper.get('[role="alert"]').text()).toBe(en.releaseError)
  expect(listIPAudit).toHaveBeenCalledTimes(2)
  expect(wrapper.find('[data-testid="release-outcome"]').exists()).toBe(false)
  wrapper.unmount()
 })
 it('keeps an unverified readback distinct from a completed release', async () => {
  vi.mocked(releaseIPAudit).mockResolvedValue({ released: false, readback: 'unavailable', remaining: [], unknown_sources: ['host_firewall'], unrestricted: false })
  const wrapper = render();await flushPromises()
  await wrapper.get('[data-testid="release-source"]').trigger('click');await wrapper.get('[data-testid="release-confirm"]').trigger('click');await flushPromises()
  expect(wrapper.get('[data-testid="release-outcome"]').text()).toContain(en.unverified)
  wrapper.unmount()
 })
 it('clearly reports a source that is still present after recovery', async () => {
  vi.mocked(releaseIPAudit).mockResolvedValue({ released: false, readback: 'still_present', remaining: [entry], unknown_sources: ['host_firewall'], unrestricted: false })
  const wrapper = render();await flushPromises()
  await wrapper.get('[data-testid="release-source"]').trigger('click');await wrapper.get('[data-testid="release-confirm"]').trigger('click');await flushPromises()
  expect(wrapper.get('[data-testid="release-outcome"]').text()).toContain(en.stillRestricted)
  expect(wrapper.get('[data-testid="release-outcome"]').text()).not.toContain(en.released)
  wrapper.unmount()
 })
 it('makes manual and disconnected sources read only, and clears stale rows on read failure', async () => {
  vi.mocked(listIPAudit).mockResolvedValue({ ...page(), items: [{ ...entry, source: 'manual_deny', can_release: false, manual: true }] })
  const wrapper = render();await flushPromises()
  expect(wrapper.find('[data-testid="release-source"]').exists()).toBe(false)
  vi.mocked(listIPAudit).mockRejectedValueOnce(new Error('offline'))
  await wrapper.get('form').trigger('submit');await flushPromises()
  expect(wrapper.get('[role="alert"]').text()).toBe(en.loadError)
  expect(wrapper.text()).not.toContain(entry.network)
  wrapper.unmount()
 })
 it('retains admin route guards, security sub-navigation, and legacy settings routes', () => {
  const router = readFileSync('src/router/index.ts', 'utf8')
  const route = router.slice(router.indexOf("path: '/admin/ip-audit'"), router.indexOf("path: '/admin/prompt-audit'"))
  expect(route).toContain('requiresAuth: true');expect(route).toContain('requiresAdmin: true')
  expect(route).not.toContain('requiresRiskControl')
  const sidebar = readFileSync('src/components/layout/AppSidebar.vue', 'utf8')
  expect(sidebar).toContain("path: '/admin/ip-audit'")
  expect(router).toContain("path: '/admin/settings'")
  expect(auditTab('ip')).toBe('ip');expect(auditTab('config')).toBe('config');expect(auditTab('unknown')).toBe('events')
 })
})
