import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import Panel from '../ModerationUserPanel.vue'
import zh from '@/i18n/locales/zh/admin/moderationUsers'
import en from '@/i18n/locales/en/admin/moderationUsers'

const { get, manage, update } = vi.hoisted(() => ({ get: vi.fn(), manage: vi.fn(), update: vi.fn() }))
vi.mock('@/api/admin/riskControl', () => ({ getModerationUser: get, manageModerationUser: manage }))
vi.mock('@/api/admin/users', () => ({ update }))
const disabled = { user_id: 306, version: 2, generation: 1, current_count: 10, auto_ban_exempt: true, keyword_exempt: true, api_audit_exempt: true, email_exempt: true, owned_disable: false, status: 'disabled', reason: 'previous review', actor_id: 17, updated_at: null }
const active = { ...disabled, status: 'active' }
function panel(locale = 'zh') {
  return mount(Panel, { props: { userId: 306 }, global: { plugins: [createI18n({ legacy: false, locale, messages: { zh: { admin: zh }, en: { admin: en } }, missingWarn: false, fallbackWarn: false })] } })
}
async function authorize(wrapper: ReturnType<typeof panel>) {
  await wrapper.get('[data-test="enable-reason"]').setValue(' reviewed disable source ')
  await wrapper.get('[data-test="enable-confirm"]').setValue(true)
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}
beforeEach(() => { vi.resetAllMocks(); get.mockResolvedValue({ ...disabled }); update.mockResolvedValue({ id: 306, status: 'active' }) })

describe('user management quick enable from moderation controls', () => {
  it.each(['zh', 'en'])('explains the independent user management action and active no-op in %s', async locale => {
    get.mockResolvedValue(active)
    const wrapper = panel(locale); await flushPromises()
    expect(wrapper.get('[data-test="enable-user"]').text()).toBe(locale === 'zh' ? '解除用户禁用（用户管理）' : 'Enable user (User management)')
    expect(wrapper.get('[data-test="enable-scope"]').text()).toContain(locale === 'zh' ? '密码、2FA' : 'Password, 2FA')
    expect(wrapper.get('[data-test="already-active"]').text()).toContain(locale === 'zh' ? '无需解除禁用' : 'no enable action needed')
    expect(wrapper.find('[data-test="enable-reason"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="enable-confirm"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="enable-user"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="enable-user"]').trigger('click')
    expect(update).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="enable-success"]').exists()).toBe(false)
  })

  it('requires its own reason and confirmation and preserves the restricted restore guard', async () => {
    const wrapper = panel(); await flushPromises()
    await wrapper.get('[data-test="reason"]').setValue('moderation action')
    await wrapper.get('[data-test="confirm"]').setValue(true)
    expect(wrapper.get('[data-test="unban"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-test="enable-user"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="enable-confirm"]').setValue(true)
    await wrapper.get('[data-test="enable-reason"]').setValue('   ')
    expect(wrapper.get('[data-test="enable-user"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="enable-reason"]').setValue('reviewed disable source')
    get.mockResolvedValueOnce(disabled).mockResolvedValueOnce(active)
    await wrapper.get('[data-test="enable-user"]').trigger('click'); await flushPromises()
    expect(update).toHaveBeenCalledTimes(1)
    expect(update).toHaveBeenCalledWith(306, { status: 'active', reason: 'reviewed disable source' })
    expect(manage).not.toHaveBeenCalled()
    expect(get).toHaveBeenCalledTimes(3)
    expect(wrapper.emitted('changed')?.[0]?.[0]).toEqual(active)
    expect(wrapper.get('[data-test="enable-success"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="enable-confirm"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="enable-reason"]').exists()).toBe(false)
  })

  it('refreshes a stale active user without writing or claiming success', async () => {
    const wrapper = panel(); await flushPromises(); await authorize(wrapper)
    get.mockResolvedValue(active)
    await wrapper.get('[data-test="enable-user"]').trigger('click'); await flushPromises()
    expect(update).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="already-active"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="enable-success"]').exists()).toBe(false)
    expect(wrapper.emitted('changed')?.[0]?.[0]).toEqual(active)
  })

  it('does not write when the fresh status cannot be read', async () => {
    const wrapper = panel(); await flushPromises(); await authorize(wrapper)
    get.mockRejectedValue(new Error('status unavailable'))
    await wrapper.get('[data-test="enable-user"]').trigger('click'); await flushPromises()
    expect(update).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('status unavailable')
    expect(wrapper.find('[data-test="enable-success"]').exists()).toBe(false)
  })

  it.each([403, 409, 500])('surfaces HTTP %s and reads current state without success', async status => {
    const wrapper = panel(); await flushPromises(); await authorize(wrapper)
    update.mockRejectedValue({ response: { status, data: { message: `enable failed ${status}` } } })
    get.mockResolvedValueOnce(disabled).mockResolvedValueOnce(active)
    await wrapper.get('[data-test="enable-user"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain(`enable failed ${status}`)
    expect(wrapper.get('[data-test="already-active"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="enable-success"]').exists()).toBe(false)
    expect(manage).not.toHaveBeenCalled()
  })

  it('does not claim success when the server readback is still disabled', async () => {
    const wrapper = panel(); await flushPromises(); await authorize(wrapper)
    await wrapper.get('[data-test="enable-user"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-test="enable-success"]').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toContain('仍为禁用')
    expect(wrapper.emitted('changed')?.[0]?.[0]).toEqual(disabled)
  })

  it('clears stale state if readback fails after a successful write', async () => {
    const wrapper = panel(); await flushPromises(); await authorize(wrapper)
    get.mockResolvedValueOnce(disabled).mockRejectedValue(new Error('readback unavailable'))
    await wrapper.get('[data-test="enable-user"]').trigger('click'); await flushPromises()
    expect(update).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[role="alert"]').text()).toContain('readback unavailable')
    expect(wrapper.find('[data-test="current-state"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="enable-success"]').exists()).toBe(false)
  })

  it('blocks duplicate submissions and ignores completion after switching UID', async () => {
    const pending = deferred<{ id: number; status: string }>()
    update.mockReturnValue(pending.promise)
    const wrapper = panel(); await flushPromises(); await authorize(wrapper)
    await wrapper.get('[data-test="enable-user"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-test="enable-user"]').trigger('click'); await flushPromises()
    expect(update).toHaveBeenCalledTimes(1)
    get.mockResolvedValue({ ...active, user_id: 307 })
    await wrapper.setProps({ userId: 307 }); await flushPromises()
    pending.resolve({ id: 306, status: 'active' }); await flushPromises()
    expect(get.mock.calls.map(call => call[0])).toEqual([306, 306, 307])
    expect(wrapper.emitted('changed')).toBeUndefined()
    expect(wrapper.find('[data-test="enable-success"]').exists()).toBe(false)
    expect(wrapper.get('legend').text()).toContain('307')
  })

  it('ignores an old preflight after switching UID and never writes to either user', async () => {
    const pending = deferred<typeof disabled>()
    const wrapper = panel(); await flushPromises(); await authorize(wrapper)
    get.mockReturnValueOnce(pending.promise)
    await wrapper.get('[data-test="enable-user"]').trigger('click'); await flushPromises()
    get.mockResolvedValue({ ...active, user_id: 307 })
    await wrapper.setProps({ userId: 307 }); await flushPromises()
    pending.resolve(disabled); await flushPromises()
    expect(update).not.toHaveBeenCalled()
    expect(wrapper.emitted('changed')).toBeUndefined()
  })
})
