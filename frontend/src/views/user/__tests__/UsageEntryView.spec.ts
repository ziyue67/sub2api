import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import UsageEntryView from '../UsageEntryView.vue'

const auth = vi.hoisted(() => ({ isObserver: false }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/views/admin/UsageView.vue', () => ({ __esModule: true, default: { props: { observerMode: Boolean }, template: '<div data-test="management">{{ observerMode }}</div>' } }))
vi.mock('@/views/user/UsageView.vue', () => ({ __esModule: true, default: { template: '<div data-test="personal" />' } }))

it.each([false, true])('selects the role-appropriate usage page (observer=%s)', async (observer) => {
  auth.isObserver = observer
  const wrapper = mount(UsageEntryView)
  await vi.dynamicImportSettled()
  await flushPromises()
  expect(wrapper.find('[data-test="personal"]').exists()).toBe(!observer)
  expect(wrapper.find('[data-test="management"]').exists()).toBe(observer)
  if (observer) expect(wrapper.text()).toBe('true')
  wrapper.unmount()
})
