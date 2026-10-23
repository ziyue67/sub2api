import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { InternalAxiosRequestConfig } from 'axios'
import { apiClient } from '@/api/client'
import SIWCAccountSettings from '../SIWCAccountSettings.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/i18n', () => ({ getLocale: () => 'zh-CN' }))
const { copyToClipboard } = vi.hoisted(() => ({ copyToClipboard: vi.fn() }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard }) }))

const props = { accountId: 42, enabled: false, active: true }
const authURL = 'https://auth.openai.com/api/accounts/authorize?state=synthetic-state&nonce=synthetic-nonce'
const callbackURL = 'http://127.0.0.1:1455/auth/callback?code=synthetic-code&state=synthetic-state'
const originalAdapter = apiClient.defaults.adapter
const adapter = vi.fn()
let status: Record<string, unknown>
let authorization: unknown
let failure: { url: string; message: string } | undefined
let callbackBody: unknown

beforeEach(() => {
  vi.resetAllMocks()
  localStorage.clear()
  status = { bound: false, enabled: false }
  authorization = { session_id: 'session-42', auth_url: authURL }
  failure = undefined
  callbackBody = undefined
  copyToClipboard.mockResolvedValue(true)
  // Keep the real SIWC API methods and Axios interceptors; mock only HTTP.
  adapter.mockImplementation(async (config: InternalAxiosRequestConfig) => {
    let data: unknown
    if (config.url?.endsWith('/authorize')) data = authorization
    else if (config.url?.endsWith('/callback')) {
      callbackBody = JSON.parse(config.data)
      status = { bound: true, enabled: false }
      data = { bound: true }
    } else data = status
    return {
      config, status: 200, statusText: 'OK', headers: {},
      data: failure?.url === config.url
        ? { code: 400, message: failure.message }
        : { code: 0, message: '', data }
    }
  })
  apiClient.defaults.adapter = adapter
})
afterEach(() => { apiClient.defaults.adapter = originalAdapter })

describe('SIWC account protocol and authorization with the HTTP response envelope', () => {
  it('requires a grant before switching and keeps selection explicit', async () => {
    const wrapper = mount(SIWCAccountSettings, { props })
    await flushPromises()
    expect(wrapper.get('option[value="siwc"]').attributes('disabled')).toBeDefined()
    status = { bound: true, enabled: false }
    await wrapper.setProps({ active: false }); await wrapper.setProps({ active: true }); await flushPromises()
    expect(wrapper.get('option[value="siwc"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.emitted('update:enabled')).toBeUndefined()
    await wrapper.get('[data-testid="siwc-protocol"]').setValue('siwc')
    expect(wrapper.emitted('update:enabled')).toEqual([[true]])
    wrapper.unmount()
  })

  it('queues a SIWC login using saved credentials without requesting passwords', async () => {
    const wrapper = mount(SIWCAccountSettings, { props }); await flushPromises()
    await wrapper.get('[data-testid="siwc-auto"]').trigger('click'); await flushPromises()
    const request = adapter.mock.calls.map(([config]) => config).find(config => config.url.endsWith('/openai-reauth'))
    expect(JSON.parse(request.data)).toEqual({ oauth_profile: 'siwc' })
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows an actionable link, the full URL and a working copy entry after a failed automatic attempt', async () => {
    status = { bound: false, enabled: false, task: { status: 'failed', stage: 'failed', error: 'previous automatic failure' } }
    const wrapper = mount(SIWCAccountSettings, { props }); await flushPromises()
    await wrapper.get('[data-testid="siwc-browser"]').trigger('click'); await flushPromises()
    const link = wrapper.get('[data-testid="siwc-open"]')
    expect(link.attributes('href')).toBe(authURL)
    expect(link.attributes('target')).toBe('_blank')
    expect(link.classes()).toContain('btn-primary')
    expect((wrapper.get('[data-testid="siwc-auth-url"]').element as HTMLTextAreaElement).value).toBe(authURL)
    await wrapper.get('[data-testid="siwc-copy"]').trigger('click'); await flushPromises()
    expect(copyToClipboard).toHaveBeenCalledWith(authURL)
    expect(wrapper.text()).not.toContain('previous automatic failure')
    expect(wrapper.get('[data-testid="siwc-complete"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('submits the full callback with the server session and refreshes binding without switching protocol', async () => {
    const wrapper = mount(SIWCAccountSettings, { props }); await flushPromises()
    await wrapper.get('[data-testid="siwc-browser"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="siwc-callback"]').setValue(`  ${callbackURL}  `)
    await wrapper.get('[data-testid="siwc-complete"]').trigger('click'); await flushPromises()
    expect(callbackBody).toEqual({ session_id: 'session-42', callback_url: callbackURL })
    expect(wrapper.get('[data-testid="siwc-status"]').text()).toBe('siwc.bound')
    expect(wrapper.find('[data-testid="siwc-browser-flow"]').exists()).toBe(false)
    expect(wrapper.get('option[value="siwc"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.emitted('update:enabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('preserves the callback and displays normalized API errors when binding is rejected', async () => {
    failure = { url: '/admin/accounts/42/siwc/callback', message: 'SIWC session expired' }
    const wrapper = mount(SIWCAccountSettings, { props }); await flushPromises()
    await wrapper.get('[data-testid="siwc-browser"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="siwc-callback"]').setValue(callbackURL)
    await wrapper.get('[data-testid="siwc-complete"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('SIWC session expired')
    expect((wrapper.get('[data-testid="siwc-callback"]').element as HTMLInputElement).value).toBe(callbackURL)
    wrapper.unmount()
  })

  it.each([
    {}, { session_id: 's' }, { auth_url: authURL },
    { session_id: 's', auth_url: 'javascript:alert(1)' },
    { session_id: 's', auth_url: 'https://untrusted.example/authorize' }
  ])('rejects malformed authorization responses without showing a broken link: %j', async result => {
    authorization = result
    const wrapper = mount(SIWCAccountSettings, { props }); await flushPromises()
    await wrapper.get('[data-testid="siwc-browser"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-testid="siwc-browser-flow"]').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toBe('siwc.invalidAuthorization')
    wrapper.unmount()
  })

  it('keeps the full link available for manual copy if the clipboard is unavailable', async () => {
    copyToClipboard.mockResolvedValue(false)
    const wrapper = mount(SIWCAccountSettings, { props }); await flushPromises()
    await wrapper.get('[data-testid="siwc-browser"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="siwc-copy"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('siwc.copyFailed')
    expect(wrapper.get('[data-testid="siwc-auth-url"]').attributes('readonly')).toBeDefined()
    wrapper.unmount()
  })

  it('clears authorization and callback when closed and ignores an old account response', async () => {
    const wrapper = mount(SIWCAccountSettings, { props }); await flushPromises()
    await wrapper.get('[data-testid="siwc-browser"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="siwc-callback"]').setValue(callbackURL)
    await wrapper.setProps({ active: false }); await flushPromises()
    expect(wrapper.find('[data-testid="siwc-browser-flow"]').exists()).toBe(false)
    expect(callbackBody).toBeUndefined()
    await wrapper.setProps({ accountId: 43, active: true }); await flushPromises()
    let resolve!: (value: unknown) => void
    adapter.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    await wrapper.get('[data-testid="siwc-browser"]').trigger('click'); await flushPromises()
    await wrapper.setProps({ accountId: 44 }); await flushPromises()
    resolve({ status: 200, statusText: 'OK', headers: {}, config: {}, data: { code: 0, data: authorization } })
    await flushPromises()
    expect(wrapper.find('[data-testid="siwc-browser-flow"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
