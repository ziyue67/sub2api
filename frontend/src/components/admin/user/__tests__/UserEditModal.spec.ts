import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import UserEditModal from '../UserEditModal.vue'

const { update, updateUserAttributeValues, showSuccess, showError, auth } = vi.hoisted(() => ({
  update: vi.fn(),
  updateUserAttributeValues: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  auth: { isSimpleMode: false }
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: { update },
    userAttributes: { updateUserAttributeValues }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn() })
}))

// useStepUp pulls in the API client, which needs the real i18n instance.
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params ? `${key}:${JSON.stringify(params)}` : key
  })
}))

const mountModal = (concurrency: number, overrides: Record<string, unknown> = {}) => mount(UserEditModal, {
  props: {
    show: true,
    user: { id: 7, email: 'user@example.test', username: 'user', notes: '', role: 'user', concurrency, rpm_limit: 0, ...overrides } as never
  },
  global: {
    stubs: {
      BaseDialog: {
        props: ['show', 'title'],
        template: '<div v-if="show"><slot /><slot name="footer" /></div>'
      },
      Select: {
        props: ['modelValue', 'options', 'searchable'],
        template: `<select data-test="role-select" :value="modelValue" @change="$emit('update:modelValue', $event.target.value)"><option value="user">user</option><option value="observer">observer</option><option value="admin">admin</option></select>`
      },
      ObserverGroupSelector: true,
      Icon: true,
      UserAttributeForm: true,
      TotpStepUpDialog: true
    }
  }
})

describe('UserEditModal concurrency', () => {
  beforeEach(() => {
    auth.isSimpleMode = false
    update.mockReset()
    updateUserAttributeValues.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    update.mockResolvedValue({})
  })

  // Regression coverage for issue #5977: the gateway treats concurrency <= 0 as
  // unlimited (AcquireUserSlot) and both the batch limits endpoint and the bulk
  // edit modal accept 0, so this dialog must not be the only place that rejects
  // it — doing so blocked every other edit on such a user.
  it('saves an unlimited (0) concurrency instead of blocking the whole form', async () => {
    const wrapper = mountModal(0)

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(showError).not.toHaveBeenCalled()
    expect(update).toHaveBeenCalledWith(7, expect.objectContaining({ concurrency: 0 }))
    expect(wrapper.emitted('success')).toBeTruthy()
  })

  it('still rejects a negative concurrency', async () => {
    const wrapper = mountModal(3)

    await wrapper.get('[data-test="concurrency-input"]').setValue('-1')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.users.concurrencyNonNegative')
    expect(update).not.toHaveBeenCalled()
  })

  it('shows three unchecked options only for a new observer promotion', async () => {
    const wrapper = mountModal(5)
    expect(wrapper.find('[data-test="observer-setup"]').exists()).toBe(false)
    await wrapper.get('[data-test="role-select"]').setValue('observer')
    const checkboxes = wrapper.findAll('[data-test="observer-setup"] input[type="checkbox"]')
    expect(checkboxes).toHaveLength(3)
    expect(checkboxes.every(input => !(input.element as HTMLInputElement).checked)).toBe(true)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(update.mock.calls[0][1]).not.toHaveProperty('observer_setup')
  })

  it('submits each selected action without directly changing balance or concurrency', async () => {
    const wrapper = mountModal(5)
    await wrapper.get('[data-test="role-select"]').setValue('observer')
    for (const selector of ['observer-create-group', 'observer-revoke-public', 'observer-grant-resources']) {
      await wrapper.get(`[data-test="${selector}"]`).setValue(true)
    }
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(update).toHaveBeenCalledWith(7, expect.objectContaining({
      role: 'observer', concurrency: 5,
      observer_setup: { create_dedicated_group: true, revoke_public_groups: true, grant_resources: true }
    }))
    expect(update.mock.calls[0][1]).not.toHaveProperty('balance')
  })

  it('allows independent selection of the public-group restriction', async () => {
    const wrapper = mountModal(5)
    await wrapper.get('[data-test="role-select"]').setValue('observer')
    await wrapper.get('[data-test="observer-revoke-public"]').setValue(true)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(update.mock.calls[0][1].observer_setup).toEqual({
      create_dedicated_group: false, revoke_public_groups: true, grant_resources: false
    })
  })

  it('requires a username when creating a dedicated group', async () => {
    const wrapper = mountModal(5, { username: ' ' })
    await wrapper.get('[data-test="role-select"]').setValue('observer')
    await wrapper.get('[data-test="observer-create-group"]').setValue(true)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(update).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.users.observerSetup.usernameRequired')
  })

  it('clears selections when changing role or reopening the dialog', async () => {
    const wrapper = mountModal(5)
    await wrapper.get('[data-test="role-select"]').setValue('observer')
    await wrapper.get('[data-test="observer-grant-resources"]').setValue(true)
    await wrapper.get('[data-test="role-select"]').setValue('user')
    await wrapper.get('[data-test="role-select"]').setValue('observer')
    expect((wrapper.get('[data-test="observer-grant-resources"]').element as HTMLInputElement).checked).toBe(false)
    await wrapper.get('[data-test="observer-grant-resources"]').setValue(true)
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    expect((wrapper.get('[data-test="observer-grant-resources"]').element as HTMLInputElement).checked).toBe(false)
  })

  it('does not offer setup actions while editing an existing observer', () => {
    const wrapper = mountModal(5, { role: 'observer' })
    expect(wrapper.find('[data-test="observer-setup"]').exists()).toBe(false)
  })

  it('disables exclusive-group creation in simple mode', async () => {
    auth.isSimpleMode = true
    const wrapper = mountModal(5)
    await wrapper.get('[data-test="role-select"]').setValue('observer')
    expect((wrapper.get('[data-test="observer-create-group"]').element as HTMLInputElement).disabled).toBe(true)
    expect(wrapper.text()).toContain('admin.users.observerSetup.simpleModeHint')
  })

  it('preserves committed grants on retry after an attribute update fails', async () => {
    update.mockResolvedValue({ role: 'observer', concurrency: 1005, observer_group_ids: [41] })
    updateUserAttributeValues.mockRejectedValueOnce(new Error('attributes failed')).mockResolvedValueOnce({})
    const wrapper = mountModal(5)
    await wrapper.get('[data-test="role-select"]').setValue('observer')
    await wrapper.get('[data-test="observer-create-group"]').setValue(true)
    await wrapper.get('[data-test="observer-grant-resources"]').setValue(true)
    wrapper.findComponent({ name: 'UserAttributeForm' }).vm.$emit('update:modelValue', { 1: 'value' })
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.emitted('close')).toBeFalsy()
    expect(wrapper.find('[data-test="observer-setup"]').exists()).toBe(false)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(update).toHaveBeenCalledTimes(2)
    expect(update.mock.calls[1][1]).toEqual(expect.objectContaining({ concurrency: 1005, observer_group_ids: [41] }))
    expect(update.mock.calls[1][1]).not.toHaveProperty('observer_setup')
    expect(wrapper.emitted('success')).toBeTruthy()
  })
})
