import { mount, flushPromises } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import FeatureSearch from '../FeatureSearch.vue'
import zhAll from '@/i18n/locales/zh'
import { buildFeatureSearchEntries, searchFeatures, type SearchNavItem } from '@/utils/featureSearch'

const items: SearchNavItem[] = [
  { path: '/admin/users', label: '用户管理' },
  { path: '/admin/channels', label: '渠道管理', expandOnly: true, children: [
    { path: '/admin/channels/pricing', label: '渠道定价' },
    { path: '/admin/channels/monitor', label: '渠道监控' }
  ] },
  { path: '/admin/settings', label: '系统设置' }
]
const entries = buildFeatureSearchEntries(items, path => path.endsWith('users') ? 'User Management' : '')

describe('feature search index', () => {
  it('flattens navigable children, carries parent keywords and deduplicates paths', () => {
    const result = buildFeatureSearchEntries([...items, items[0]!])
    expect(result).toHaveLength(4)
    expect(result.some(item => item.path === '/admin/channels')).toBe(false)
    expect(searchFeatures(result, '渠道管理')).toHaveLength(2)
  })
  it('matches Chinese, English, paths, full-width input and multiple keywords', () => {
    for (const query of ['用户', 'ＵＳＥＲ', 'user MANAGEMENT', '/admin/users']) {
      expect(searchFeatures(entries, query).map(item => item.path)).toEqual(['/admin/users'])
    }
    expect(searchFeatures(entries, '渠道 监控')[0]?.path).toBe('/admin/channels/monitor')
    expect(searchFeatures(entries, '不存在')).toEqual([])
    expect(searchFeatures(entries, '  ')).toHaveLength(4)
  })
  it('never adds a route that was not supplied by the visible navigation', () => {
    const visible = buildFeatureSearchEntries([items[0]!])
    expect(searchFeatures(visible, 'settings')).toEqual([])
    expect(searchFeatures(visible, '')).toHaveLength(1)
  })
})

let cleanup: (() => void) | undefined
afterEach(() => { cleanup?.(); cleanup = undefined; document.body.innerHTML = ''; vi.restoreAllMocks() })

function messageFunctions(value: unknown): unknown {
  return typeof value === 'string' ? () => value : Object.fromEntries(Object.entries(value as Record<string, unknown>).map(([key, child]) => [key, messageFunctions(child)]))
}

async function setup() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      ...entries.map(item => ({ path: item.path, component: { template: '<div />' }, meta: { title: item.path.endsWith('users') ? 'User Management' : item.label } }))
    ]
  })
  await router.push('/')
  await router.isReady()
  const wrapper = mount(FeatureSearch, {
    props: { items }, attachTo: document.body,
    global: {
      stubs: { transition: true },
      plugins: [router, createI18n({ legacy: false, locale: 'zh', messages: {
        zh: messageFunctions(zhAll) as typeof zhAll
      } })]
    }
  })
  cleanup = () => wrapper.unmount()
  Element.prototype.scrollIntoView = vi.fn()
  return { wrapper, router }
}

async function key(target: EventTarget, value: string, options: KeyboardEventInit = {}) {
  target.dispatchEvent(new KeyboardEvent('keydown', { key: value, bubbles: true, cancelable: true, ...options }))
  await flushPromises()
}

describe('feature search dialog', () => {
  it('opens from shortcut, focuses input, navigates with arrows and Enter, then closes', async () => {
    const { wrapper, router } = await setup()
    await key(document, 'k', { ctrlKey: true })
    const input = document.querySelector<HTMLInputElement>('[role=combobox]')!
    expect(document.activeElement).toBe(input)
    await key(input, 'ArrowDown')
    expect(input.getAttribute('aria-activedescendant')).toBe('feature-search-option-1')
    await key(input, 'Enter')
    expect(router.currentRoute.value.path).toBe('/admin/channels/pricing')
    expect(wrapper.emitted('navigate')).toEqual([['/admin/channels/pricing']])
    expect(document.querySelector('[role=dialog]')).toBeNull()
  })
  it('finds cyber and navigates directly to the risk settings card', async () => {
    const { wrapper, router } = await setup()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    const input = document.querySelector<HTMLInputElement>('[role=combobox]')!
    input.value = 'cyber'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    expect(document.querySelectorAll('[role=option]')).toHaveLength(1)
    expect(document.querySelector('[role=option]')?.textContent).toContain('风控中心')
    await key(input, 'Enter')
    expect(router.currentRoute.value.fullPath).toBe('/admin/settings?tab=features#settings-section-features-risk-control')
    const heading = document.createElement('h2')
    heading.id = 'settings-section-features-risk-control'
    heading.tabIndex = -1
    document.body.appendChild(heading)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    const repeatedInput = document.querySelector<HTMLInputElement>('[role=combobox]')!
    repeatedInput.value = '风控'
    repeatedInput.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    await key(repeatedInput, 'Enter')
    expect(document.activeElement).toBe(heading)
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled()
  })
  it('updates results when visible navigation changes and safely handles empty results and IME', async () => {
    const { wrapper, router } = await setup()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    const input = document.querySelector<HTMLInputElement>('[role=combobox]')!
    await key(input, 'Enter', { isComposing: true })
    expect(router.currentRoute.value.path).toBe('/')
    await wrapper.setProps({ items: [items[0]!] })
    input.value = 'settings'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    expect(document.querySelectorAll('[role=option]')).toHaveLength(0)
    await key(input, 'ArrowDown')
    await key(input, 'Enter')
    expect(router.currentRoute.value.path).toBe('/')
    await key(input, 'Escape')
    expect(document.querySelector('[role=dialog]')).toBeNull()
  })
  it('supports Command K, traps Tab, restores focus and does not stack over another modal', async () => {
    const { wrapper } = await setup()
    const trigger = wrapper.get('button').element
    trigger.focus()
    await key(document, 'k', { metaKey: true })
    const input = document.querySelector<HTMLInputElement>('[role=combobox]')!
    const close = document.querySelector<HTMLButtonElement>('[aria-label="Close modal"]')!
    await key(input, 'Tab')
    expect(document.activeElement).toBe(close)
    await key(close, 'Tab', { shiftKey: true })
    expect(document.activeElement).toBe(input)
    await key(input, 'Escape')
    expect(document.activeElement).toBe(trigger)
    const modal = document.createElement('div')
    modal.setAttribute('role', 'dialog')
    modal.setAttribute('aria-modal', 'true')
    document.body.appendChild(modal)
    await key(document, 'k', { ctrlKey: true })
    expect(document.querySelector('[role=combobox]')).toBeNull()
  })
})
