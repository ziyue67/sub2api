import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import IPRiskSettings from '../IPRiskSettings.vue'
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
  return mount(IPRiskSettings, { global: { plugins: [createI18n({ legacy: false, locale: 'zh', messageCompiler: message => () => String(message), messages: { zh: { admin: { settings: { ipRisk: zh } } }, en: { admin: { settings: { ipRisk: en } } } } })] } })
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
  vi.mocked(ipRiskAPI.events).mockResolvedValue([])
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
    await wrapper.get('[data-testid="risk-add-rule"]').trigger('click')
    await wrapper.get('[data-testid="risk-rule-0"]').setValue('198.51.100.0/24')
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
    expect(ipRiskAPI.events).toHaveBeenLastCalledWith('', '', 'login')
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
    vi.mocked(ipRiskAPI.events).mockResolvedValue([record])
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
