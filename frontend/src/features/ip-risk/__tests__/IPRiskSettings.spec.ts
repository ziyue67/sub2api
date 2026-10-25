import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import IPRiskSettings from '../IPRiskSettings.vue'
import Pagination from '@/components/common/Pagination.vue'
import zhDashboard from '@/i18n/locales/zh/dashboard'
import enDashboard from '@/i18n/locales/en/dashboard'
import { ipRiskAPI, type IPRiskSnapshot } from '../api'
import zh from '@/i18n/locales/zh/admin/ipRisk'
import en from '@/i18n/locales/en/admin/ipRisk'

vi.mock('../api', () => ({ ipRiskAPI: { get: vi.fn(), anonymousBans: vi.fn(), unbanAnonymous: vi.fn(), save: vi.fn(), events: vi.fn(), bans: vi.fn(), unban: vi.fn(), clearEvents: vi.fn(), check: vi.fn() } }))

function snapshot(): IPRiskSnapshot {
  return {
    current_ip: '203.0.113.10', trusted_proxies_configured: false,
    health: { redis_available: true, emergency_disabled: false, config_error: '' },
    config: {
      anonymous_api: { enabled: false, threshold: 30, window_seconds: 60, ban_seconds: 300, max_tracked_ips: 10000, reset_on_success: false },
      enabled: false, observe_only: true, whitelist: [], blacklist: [], allowed_countries: [], blocked_countries: [],
      block_unknown_country: false, deny_user_agents: ['python-requests'], block_empty_ua: false,
      ipv6_prefix: 64, ban_seconds: 3600, event_retention_hours: 168,
      login_attempts: { limit: 60, window_seconds: 60 }, login_failures: { limit: 10, window_seconds: 900 },
      register_attempts: { limit: 10, window_seconds: 600 }, register_failures: { limit: 5, window_seconds: 600 },
      verification_attempts: { limit: 10, window_seconds: 600 }
    }
  }
}
function create() {
  return mount(IPRiskSettings, { global: { stubs: { teleport: true }, plugins: [createI18n({ legacy: false, locale: 'zh', messageCompiler: message => () => String(message), messages: { zh: { ...zhDashboard, admin: { settings: { ipRisk: zh } } }, en: { ...enDashboard, admin: { settings: { ipRisk: en } } } } })] } })
}
async function opened() {
  const wrapper = create()
  ;(wrapper.get('details').element as HTMLDetailsElement).open = true
  await wrapper.get('details').trigger('toggle'); await flushPromises()
  return wrapper
}
function button(wrapper: ReturnType<typeof create>, text: string) {
  const result = wrapper.findAll('button').find(b => b.text() === text)
  if (!result) throw new Error('Missing button: ' + text)
  return result
}
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(ipRiskAPI.get).mockResolvedValue(snapshot())
  vi.mocked(ipRiskAPI.save).mockImplementation(async config => structuredClone(config))
  vi.mocked(ipRiskAPI.events).mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
  vi.mocked(ipRiskAPI.bans).mockResolvedValue([])
  vi.mocked(ipRiskAPI.anonymousBans).mockResolvedValue([])
})

describe('IP risk settings', () => {
  it('edits anonymous protection independently and releases only its temporary ban', async () => {
    vi.mocked(ipRiskAPI.anonymousBans).mockResolvedValue([{ network: '8.8.8.8/32', reason: 'anonymous_api_unknown', created_at: 1, expires_at: 2, observe_only: false }])
    const wrapper = await opened()
    await wrapper.get('[data-testid="anonymous-unban"]').trigger('click'); await flushPromises()
    expect(ipRiskAPI.unbanAnonymous).toHaveBeenCalledWith('8.8.8.8/32')
    expect(ipRiskAPI.unban).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="anonymous-release-ip"]').setValue(' 1.1.1.1 ')
    await wrapper.get('[data-testid="anonymous-release-manual"]').trigger('click'); await flushPromises()
    expect(ipRiskAPI.unbanAnonymous).toHaveBeenLastCalledWith('1.1.1.1')
    expect((wrapper.get('[data-testid="anonymous-enabled"]').element as HTMLInputElement).checked).toBe(false)
    await wrapper.get('[data-testid="anonymous-enabled"]').setValue(true)
    await wrapper.get('[data-testid="anonymous-threshold"]').setValue('40')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    const payload = vi.mocked(ipRiskAPI.save).mock.calls[0]![0]
    expect(payload.enabled).toBe(false)
    expect(payload.anonymous_api.enabled).toBe(true)
    expect(payload.anonymous_api.threshold).toBe(40)
    wrapper.unmount()
  })

  it('opens the existing settings component from the security deep link', async () => {
    const original = window.location.href
    window.history.replaceState({}, '', '/admin/settings?tab=security#ip-risk-settings')
    try {
      const wrapper = create();await flushPromises()
      expect((wrapper.get('details').element as HTMLDetailsElement).open).toBe(true)
      expect(ipRiskAPI.get).toHaveBeenCalledOnce()
      wrapper.unmount()
    } finally { window.history.replaceState({}, '', original) }
  })

  it('loads only when opened and preserves safe initial defaults', async () => {
    const wrapper = create(); await flushPromises()
    expect(ipRiskAPI.get).not.toHaveBeenCalled()
    expect(wrapper.find('form').exists()).toBe(false)
    wrapper.unmount()
    const loaded = await opened()
    expect(ipRiskAPI.get).toHaveBeenCalledOnce()
    expect((loaded.get('[data-testid="risk-enabled"]').element as HTMLInputElement).checked).toBe(false)
    expect((loaded.get('[data-testid="risk-observe"]').element as HTMLInputElement).checked).toBe(true)
    expect(loaded.text()).toContain(zh.proxyWarning)
    loaded.unmount()
  })

  it('saves edited reactive rules, deduplicated whitelist and country codes', async () => {
    const wrapper = await opened()
    await wrapper.get('[data-testid="risk-enabled"]').setValue(true)
    await wrapper.get('[data-testid="risk-whitelist"]').setValue(' 192.0.2.1\n192.0.2.1\n2001:db8::/64 ')
    await wrapper.get('[data-testid="risk-whitelist-current"]').trigger('click')
    await wrapper.get('[data-testid="risk-countries"]').setValue(' us, jp,us ')
    await wrapper.get('[data-testid="risk-manage-blacklist"]').trigger('click')
    await wrapper.get('[data-testid="risk-add-rule"]').trigger('click')
    await wrapper.get('[data-testid="risk-rule-network"]').setValue('198.51.100.0/24')
    await wrapper.get('[data-testid="risk-rule-form"]').trigger('submit')
    await wrapper.get('[data-testid="risk-blacklist-apply"]').trigger('click')
    await wrapper.get('[data-testid="risk-login_failures-limit"]').setValue('7')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(ipRiskAPI.save).toHaveBeenCalledOnce()
    const payload = vi.mocked(ipRiskAPI.save).mock.calls[0]![0]
    expect(payload.enabled).toBe(true)
    expect(payload.observe_only).toBe(true)
    expect(payload.whitelist).toEqual(['192.0.2.1', '2001:db8::/64', '203.0.113.10'])
    expect(payload.blocked_countries).toEqual(['US', 'JP'])
    expect(payload.blacklist).toEqual([{ network: '198.51.100.0/24', enabled: true, note: '', expires_at: 0 }])
    expect(payload.login_failures.limit).toBe(7)
    expect(wrapper.get('[role="status"]').text()).toBe(zh.saved)
    wrapper.unmount()
  })

  it('maintains blacklist rules in a dialog without saving or changing other drafts', async () => {
    const current = snapshot()
    current.config.blacklist = [{ network: '198.51.100.1', note: 'original', enabled: true, expires_at: 2000000037 }]
    vi.mocked(ipRiskAPI.get).mockResolvedValue(current)
    const wrapper = await opened()
    await wrapper.get('[data-testid="risk-whitelist"]').setValue('192.0.2.55')
    expect(wrapper.find('[data-testid="risk-rule-network"]').exists()).toBe(false)
    await wrapper.get('[data-testid="risk-manage-blacklist"]').trigger('click')
    expect(wrapper.get('[role="dialog"]').text()).toContain(zh.blacklistDraftHint)
    await wrapper.get('[data-testid="risk-rule-edit-0"]').trigger('click')
    await wrapper.get('[data-testid="risk-rule-note"]').setValue('updated')
    await wrapper.get('[data-testid="risk-rule-enabled"]').setValue(false)
    await wrapper.get('[data-testid="risk-rule-form"]').trigger('submit')
    await wrapper.get('[data-testid="risk-add-rule"]').trigger('click')
    await wrapper.get('[data-testid="risk-rule-network"]').setValue(' 203.0.113.0/24 ')
    await wrapper.get('[data-testid="risk-rule-form"]').trigger('submit')
    await wrapper.get('[data-testid="risk-blacklist-apply"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(ipRiskAPI.save).not.toHaveBeenCalled()
    expect(wrapper.get('[role="status"]').text()).toBe(zh.blacklistApplied)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(ipRiskAPI.save).toHaveBeenLastCalledWith(expect.objectContaining({
      whitelist: ['192.0.2.55'],
      blacklist: [
        { network: '198.51.100.1', note: 'updated', enabled: false, expires_at: 2000000037 },
        { network: '203.0.113.0/24', note: '', enabled: true, expires_at: 0 }
      ]
    }))
    wrapper.unmount()
  })

  it('discards dialog edits on Cancel or Escape and can apply removal of all rules', async () => {
    const current = snapshot()
    current.config.blacklist = [{ network: '198.51.100.1', note: 'original', enabled: true, expires_at: 0 }]
    vi.mocked(ipRiskAPI.get).mockResolvedValue(current)
    const wrapper = await opened()
    await wrapper.get('[data-testid="risk-manage-blacklist"]').trigger('click')
    await wrapper.get('[data-testid="risk-rule-remove-0"]').trigger('click')
    await wrapper.get('[data-testid="risk-blacklist-cancel"]').trigger('click')
    await wrapper.get('[data-testid="risk-manage-blacklist"]').trigger('click')
    expect(wrapper.get('[data-testid="risk-rule-row-0"]').text()).toContain('198.51.100.1')
    await wrapper.get('[data-testid="risk-rule-edit-0"]').trigger('click')
    await wrapper.get('[data-testid="risk-rule-network"]').setValue('203.0.113.10')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })); await flushPromises()
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    await wrapper.get('[data-testid="risk-manage-blacklist"]').trigger('click')
    expect(wrapper.get('[data-testid="risk-rule-row-0"]').text()).toContain('198.51.100.1')
    await wrapper.get('[data-testid="risk-rule-remove-0"]').trigger('click')
    await wrapper.get('[data-testid="risk-blacklist-apply"]').trigger('click')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(ipRiskAPI.save).toHaveBeenLastCalledWith(expect.objectContaining({ blacklist: [] }))
    wrapper.unmount()
  })

  it('lists rules with actions and isolates add/edit forms until each rule is saved', async () => {
    const current = snapshot()
    current.config.blacklist = [
      { network: '198.51.100.1', note: 'first', enabled: true, expires_at: 0 },
      { network: '203.0.113.2', note: 'second', enabled: false, expires_at: 2000000037 }
    ]
    vi.mocked(ipRiskAPI.get).mockResolvedValue(current)
    const wrapper = await opened()
    await wrapper.get('[data-testid="risk-manage-blacklist"]').trigger('click')
    const table = () => wrapper.get('[data-testid="risk-blacklist-table"]')
    expect(table().findAll('tbody tr')).toHaveLength(2)
    expect(table().findAll('input')).toHaveLength(0)
    expect(table().get('thead').text()).toContain(zh.ruleActions)
    expect(table().get('tbody tr').text()).toContain(zh.permanent)
    expect(wrapper.get('[data-testid="risk-rule-row-1"]').text()).toContain(zh.protectionDisabled)

    await wrapper.get('[data-testid="risk-add-rule"]').trigger('click')
    expect(wrapper.find('[data-testid="risk-blacklist-apply"]').exists()).toBe(false)
    expect((wrapper.get('[data-testid="risk-rule-network"]').element as HTMLInputElement).checkValidity()).toBe(false)
    await wrapper.get('[data-testid="risk-rule-form"]').trigger('submit')
    expect(wrapper.find('[data-testid="risk-rule-form"]').exists()).toBe(true)
    await wrapper.get('[data-testid="risk-rule-network"]').setValue('192.0.2.5')
    await wrapper.get('[data-testid="risk-rule-cancel"]').trigger('click')
    expect(table().findAll('tbody tr')).toHaveLength(2)
    expect(table().text()).not.toContain('192.0.2.5')

    await wrapper.get('[data-testid="risk-rule-edit-0"]').trigger('click')
    await wrapper.get('[data-testid="risk-rule-note"]').setValue('discard this edit')
    await wrapper.get('[data-testid="risk-rule-cancel"]').trigger('click')
    expect(table().text()).not.toContain('discard this edit')
    await wrapper.get('[data-testid="risk-rule-remove-0"]').trigger('click')
    await wrapper.get('[data-testid="risk-rule-edit-0"]').trigger('click')
    expect((wrapper.get('[data-testid="risk-rule-network"]').element as HTMLInputElement).value).toBe('203.0.113.2')
    await wrapper.get('[data-testid="risk-rule-note"]').setValue('updated remaining rule')
    await wrapper.get('[data-testid="risk-rule-form"]').trigger('submit')
    expect(wrapper.get('[data-testid="risk-blacklist-table"]').findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.get('[data-testid="risk-blacklist-table"]').text()).toContain('updated remaining rule')
    await wrapper.get('[data-testid="risk-blacklist-apply"]').trigger('click')
    expect(ipRiskAPI.save).not.toHaveBeenCalled()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(ipRiskAPI.save).toHaveBeenLastCalledWith(expect.objectContaining({ blacklist: [
      { network: '203.0.113.2', note: 'updated remaining rule', enabled: false, expires_at: 2000000037 }
    ] }))
    wrapper.unmount()
  })

  it('edits and clears rule expiration in local time without saving on dialog apply', async () => {
    const current = snapshot()
    current.config.blacklist = [{ network: '198.51.100.1', note: '', enabled: true, expires_at: 0 }]
    vi.mocked(ipRiskAPI.get).mockResolvedValue(current)
    const wrapper = await opened()
    await wrapper.get('[data-testid="risk-manage-blacklist"]').trigger('click')
    await wrapper.get('[data-testid="risk-rule-edit-0"]').trigger('click')
    await wrapper.get('[data-testid="risk-rule-expiry"]').setValue('2030-05-06T12:30')
    await wrapper.get('[data-testid="risk-rule-form"]').trigger('submit')
    await wrapper.get('[data-testid="risk-blacklist-apply"]').trigger('click')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(vi.mocked(ipRiskAPI.save).mock.calls[0]![0].blacklist[0]!.expires_at).toBe(new Date('2030-05-06T12:30').getTime() / 1000)
    await wrapper.get('[data-testid="risk-manage-blacklist"]').trigger('click')
    await wrapper.get('[data-testid="risk-rule-edit-0"]').trigger('click')
    await wrapper.get('[data-testid="risk-rule-expiry"]').setValue('')
    await wrapper.get('[data-testid="risk-rule-form"]').trigger('submit')
    await wrapper.get('[data-testid="risk-blacklist-apply"]').trigger('click')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(vi.mocked(ipRiskAPI.save).mock.calls[1]![0].blacklist[0]!.expires_at).toBe(0)
    wrapper.unmount()
  })

  it('collapses tools independently and preserves edited values when closed', async () => {
    const wrapper = await opened()
    for (const id of ['risk-country-settings', 'risk-ua-settings', 'risk-automatic-settings', 'anonymous-settings', 'anonymous-bans', 'risk-check-settings', 'risk-bans']) {
      expect((wrapper.get(`[data-testid="${id}"]`).element as HTMLDetailsElement).open).toBe(false)
    }
    const automatic = wrapper.get('[data-testid="risk-automatic-settings"]')
    await automatic.get('summary').trigger('click')
    expect((automatic.element as HTMLDetailsElement).open).toBe(true)
    await wrapper.get('[data-testid="risk-login_failures-limit"]').setValue(17)
    await automatic.get('summary').trigger('click')
    expect((automatic.element as HTMLDetailsElement).open).toBe(false)
    const anonymous = wrapper.get('[data-testid="anonymous-settings"]')
    await anonymous.get('summary').trigger('click')
    await wrapper.get('[data-testid="anonymous-enabled"]').setValue(true)
    await wrapper.get('[data-testid="anonymous-threshold"]').setValue(45)
    await anonymous.get('summary').trigger('click')
    expect(anonymous.get('summary').text()).toContain(zh.protectionEnabled)
    expect(ipRiskAPI.save).not.toHaveBeenCalled()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    const payload = vi.mocked(ipRiskAPI.save).mock.calls[0]![0]
    expect(payload.login_failures.limit).toBe(17)
    expect(payload.anonymous_api).toMatchObject({ enabled: true, threshold: 45 })
    expect((automatic.element as HTMLDetailsElement).open).toBe(false)
    wrapper.unmount()
  })

  it('reveals a collapsed section when native form validation rejects one of its fields', async () => {
    const wrapper = await opened()
    const automatic = wrapper.get('[data-testid="risk-automatic-settings"]')
    await wrapper.get('[data-testid="risk-login_failures-limit"]').setValue(-1)
    await wrapper.get('[data-testid="risk-login_failures-limit"]').trigger('invalid')
    expect((automatic.element as HTMLDetailsElement).open).toBe(true)
    expect(ipRiskAPI.save).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('sets China-only access without activating enforcement or retaining a conflicting CN block', async () => {
    const wrapper = await opened()
    await wrapper.get('[data-testid="risk-countries"]').setValue('cn, US')
    await wrapper.get('[data-testid="risk-china-only"]').trigger('click')
    expect(ipRiskAPI.save).not.toHaveBeenCalled()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    const payload = vi.mocked(ipRiskAPI.save).mock.calls[0]![0]
    expect(payload.allowed_countries).toEqual(['CN'])
    expect(payload.blocked_countries).toEqual(['US'])
    expect(payload.enabled).toBe(false)
    expect(payload.observe_only).toBe(true)
    wrapper.unmount()
  })

  it('refreshes records without overwriting unsaved rules', async () => {
    const wrapper = await opened()
    await wrapper.get('[data-testid="risk-whitelist"]').setValue('198.51.100.10')
    await wrapper.get('[data-testid="risk-category"]').setValue('login')
    await button(wrapper, zh.refreshRecords).trigger('click'); await flushPromises()
    expect((wrapper.get('[data-testid="risk-whitelist"]').element as HTMLTextAreaElement).value).toBe('198.51.100.10')
    expect(ipRiskAPI.get).toHaveBeenCalledOnce()
    expect(ipRiskAPI.events).toHaveBeenLastCalledWith('', '', 'login', 1, 20)
    wrapper.unmount()
  })

  it('paginates matching records and resets the page when filters or page size change', async () => {
    vi.mocked(ipRiskAPI.events).mockImplementation(async (_ip, _reason, _category, page = 1, pageSize = 20) => ({
      items: [], total: 65, page, page_size: pageSize, pages: Math.ceil(65 / pageSize)
    }))
    const wrapper = await opened()
    const pagination = () => wrapper.getComponent(Pagination)
    expect(pagination().props()).toMatchObject({ total: 65, page: 1, pageSize: 20 })
    await wrapper.get(`button[aria-label="${zhDashboard.pagination.next}"]`).trigger('click')
    await flushPromises()
    expect(ipRiskAPI.events).toHaveBeenLastCalledWith('', '', '', 2, 20)
    expect(pagination().props('page')).toBe(2)
    expect(ipRiskAPI.bans).toHaveBeenCalledOnce()

    await wrapper.get('[data-testid="risk-category"]').setValue('login')
    await wrapper.get(`input[aria-label="${zh.filterIP}"]`).setValue(' 198.51.100.1 ')
    await wrapper.get(`select[aria-label="${zh.filterReason}"]`).setValue('login_failures')
    await button(wrapper, zh.filter).trigger('click'); await flushPromises()
    expect(ipRiskAPI.events).toHaveBeenLastCalledWith('198.51.100.1', 'login_failures', 'login', 1, 20)

    // Unsubmitted filter edits must not silently change the next page's query.
    await wrapper.get('[data-testid="risk-category"]').setValue('register')
    pagination().vm.$emit('update:page', 3); await flushPromises()
    expect(ipRiskAPI.events).toHaveBeenLastCalledWith('198.51.100.1', 'login_failures', 'login', 3, 20)
    pagination().vm.$emit('update:pageSize', 50); await flushPromises()
    expect(ipRiskAPI.events).toHaveBeenLastCalledWith('198.51.100.1', 'login_failures', 'login', 1, 50)
    expect(pagination().props()).toMatchObject({ page: 1, pageSize: 50 })
    wrapper.unmount()
  })

  it('keeps the displayed page after a failed request and accepts a server-adjusted page', async () => {
    vi.mocked(ipRiskAPI.events).mockResolvedValue({ items: [], total: 65, page: 2, page_size: 20, pages: 4 })
    const wrapper = await opened()
    vi.mocked(ipRiskAPI.events).mockRejectedValueOnce(new Error('Records unavailable'))
    wrapper.getComponent(Pagination).vm.$emit('update:page', 3); await flushPromises()
    expect(wrapper.getComponent(Pagination).props('page')).toBe(2)
    expect(wrapper.get('[role="alert"]').text()).toContain('Records unavailable')
    vi.mocked(ipRiskAPI.events).mockResolvedValueOnce({ items: [], total: 5, page: 1, page_size: 20, pages: 1 })
    wrapper.getComponent(Pagination).vm.$emit('update:page', 3); await flushPromises()
    expect(wrapper.getComponent(Pagination).props()).toMatchObject({ page: 1, total: 5 })
    wrapper.unmount()
  })

  it('preserves the draft and displays the server self-lockout error', async () => {
    const wrapper = await opened()
    vi.mocked(ipRiskAPI.save).mockRejectedValue({ message: 'blacklist would block your current IP' })
    await wrapper.get('[data-testid="risk-whitelist"]').setValue('192.0.2.11')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('blacklist would block your current IP')
    expect((wrapper.get('[data-testid="risk-whitelist"]').element as HTMLTextAreaElement).value).toBe('192.0.2.11')
    wrapper.unmount()
  })

  it('checks the saved policy without implicitly saving or changing enabled state', async () => {
    const wrapper = await opened()
    vi.mocked(ipRiskAPI.check).mockResolvedValue({ ip: '203.0.113.10', network: '203.0.113.10/32', country: 'US', decision: 'would_block', reason: 'country_blocked' } as Awaited<ReturnType<typeof ipRiskAPI.check>>)
    await wrapper.get('[data-testid="risk-countries"]').setValue('JP')
    await wrapper.get('[data-testid="risk-check"]').trigger('click'); await flushPromises()
    expect(ipRiskAPI.check).toHaveBeenCalledWith('203.0.113.10', '', true)
    expect(ipRiskAPI.save).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain(zh.labels.would_block)
    wrapper.unmount()
  })

  it('renders UA as text, unbans a network and confirms event deletion', async () => {
    const record = { id: 'event1', at: 1700000000, ip: '198.51.100.1', network: '198.51.100.1/32', peer: '127.0.0.1', ip_source: 'trusted_proxy_chain', country: '', method: 'POST', category: 'login', path: '/api/v1/auth/login', user_agent: '<script>alert(1)</script>', status: 401, reason: 'login_failures', error_reason: 'INVALID_CREDENTIALS', decision: 'auto_banned' }
    vi.mocked(ipRiskAPI.events).mockResolvedValue({ items: [record], total: 1, page: 1, page_size: 20, pages: 1 })
    vi.mocked(ipRiskAPI.bans).mockResolvedValue([{ network: '198.51.100.1/32', reason: 'login_failures', created_at: 1700000000, expires_at: 1700003600, observe_only: false }])
    const wrapper = await opened()
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.text()).toContain('<script>alert(1)</script>')
    await button(wrapper, zh.unban).trigger('click'); await flushPromises()
    expect(ipRiskAPI.unban).toHaveBeenCalledWith('198.51.100.1/32')
    await button(wrapper, zh.clearEvents).trigger('click')
    expect(ipRiskAPI.clearEvents).not.toHaveBeenCalled()
    await button(wrapper, zh.confirm).trigger('click'); await flushPromises()
    expect(ipRiskAPI.clearEvents).toHaveBeenCalledOnce()
    expect(wrapper.findComponent(Pagination).exists()).toBe(false)
    expect(wrapper.text()).toContain(zh.noEvents)
    wrapper.unmount()
  })

  it('still permits configuration repair if Redis records cannot load', async () => {
    vi.mocked(ipRiskAPI.events).mockRejectedValue(new Error('Redis unavailable'))
    const wrapper = await opened()
    expect(wrapper.get('form').exists()).toBe(true)
    expect(wrapper.get('[role="alert"]').text()).toContain('Redis unavailable')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(ipRiskAPI.save).toHaveBeenCalledOnce()
    wrapper.unmount()
  })
})
