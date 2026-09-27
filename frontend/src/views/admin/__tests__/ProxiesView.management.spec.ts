import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises, type VueWrapper } from '@vue/test-utils'
import ProxiesView from '../ProxiesView.vue'
const { route, replace, list } = vi.hoisted(() => ({ route: { hash: '' }, replace: vi.fn(), list: vi.fn() }))
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ replace }) }))
vi.mock('@/api/admin', () => ({ adminAPI: { proxies: { list, getAllWithCount: vi.fn().mockResolvedValue([]) } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
let wrapper: VueWrapper
const mountView = () => shallowMount(ProxiesView, { global: { stubs: {
  AppLayout: { template: '<div><slot /></div>' },
  TablePageLayout: { template: '<div><slot name="actions" /><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' }
} } })
beforeEach(() => { vi.clearAllMocks(); route.hash = ''; list.mockResolvedValue({ items: [], total: 0, pages: 1 }) })
afterEach(() => wrapper?.unmount())
describe('IP management navigation', () => {
  it('preserves the existing proxy list and exposes the four management sections', async () => {
    wrapper = mountView(); await flushPromises()
    expect(wrapper.find('input[type="text"]').exists()).toBe(true)
    for (const [key, label] of [['subscriptions', 'subscriptions'], ['dynamic', 'dynamicProxies'], ['nodes', 'nodes'], ['kernel', 'kernelRules']]) {
      await wrapper.findAll('button').find(b => b.text() === 'admin.proxies.' + label)!.trigger('click')
      expect(wrapper.findComponent({ name: 'MihomoSettings' }).props('section')).toBe(key)
      expect(wrapper.find('select-stub').exists()).toBe(false)
      expect(replace).toHaveBeenLastCalledWith({ hash: '#' + key })
    }
  })
  it.each(['#subscriptions', '#mihomo'])('opens subscription management from %s', async hash => {
    route.hash = hash; wrapper = mountView(); await flushPromises()
    expect(wrapper.findComponent({ name: 'MihomoSettings' }).props('section')).toBe('subscriptions')
  })
})
