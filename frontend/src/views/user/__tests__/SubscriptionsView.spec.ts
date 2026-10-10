import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import SubscriptionsView from '../SubscriptionsView.vue'

const mocks = vi.hoisted(() => ({
  push: vi.fn(),
  app: {
    cachedPublicSettings: { payment_enabled: false },
    fetchPublicSettings: vi.fn(),
    showInfo: vi.fn(),
    showError: vi.fn(),
  },
}))

vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks.app }))
vi.mock('@/api/subscriptions', () => ({
  default: {
    getMySubscriptions: vi.fn().mockResolvedValue([
      { id: 1, group_id: 7, status: 'active', group: { name: 'Test', platform: 'openai' } },
    ]),
  },
}))

describe('subscription renewal navigation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.app.cachedPublicSettings = { payment_enabled: false }
    mocks.app.fetchPublicSettings.mockResolvedValue(null)
  })

  async function clickRenew() {
    const wrapper = shallowMount(SubscriptionsView, {
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' } } },
    })
    await flushPromises()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    wrapper.unmount()
  }

  it('stays on subscriptions and explains when payment is disabled', async () => {
    await clickRenew()
    expect(mocks.app.fetchPublicSettings).toHaveBeenCalledWith(true)
    expect(mocks.push).not.toHaveBeenCalled()
    expect(mocks.app.showInfo).toHaveBeenCalledWith('purchase.notEnabledDesc')
  })

  it('uses refreshed settings and opens the matching subscription group', async () => {
    mocks.app.fetchPublicSettings.mockImplementation(async () => {
      mocks.app.cachedPublicSettings = { payment_enabled: true }
    })
    await clickRenew()
    expect(mocks.push).toHaveBeenCalledWith({
      path: '/purchase', query: { tab: 'subscription', group: '7' },
    })
    expect(mocks.app.showInfo).not.toHaveBeenCalled()
  })
})
