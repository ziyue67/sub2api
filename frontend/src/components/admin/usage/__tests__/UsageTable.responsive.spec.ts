import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import UsageTable from '../UsageTable.vue'

vi.mock('@/stores/app', () => ({ useAppStore: () => ({}) }))
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
const row = { id: 1, model: 'fixture', input_tokens: 100, output_tokens: 20, cache_read_tokens: 0, cache_creation_tokens: 0, actual_cost: 0.12, total_cost: 0.2, account_rate_multiplier: 2 }
const wrappers: ReturnType<typeof mount>[] = []
function resize(width: number) {
  Object.defineProperty(window, 'innerWidth', { value: width, configurable: true })
  window.dispatchEvent(new Event('resize'))
}
function render(showAccountBilling = true) {
  const wrapper = mount(UsageTable, {
    attachTo: document.body,
    props: { data: [row], columns: [], showAccountBilling },
    global: { stubs: {
      DataTable: { props: ['data'], template: '<div><slot name="cell-tokens" :row="data[0]"/><slot name="cell-cost" :row="data[0]"/></div>' },
      Icon: true, UsageTimingDialog: true, transition: true,
    } },
  })
  wrappers.push(wrapper)
  return wrapper
}
afterEach(() => { wrappers.forEach(w => w.unmount()); wrappers.length = 0; vi.restoreAllMocks(); document.body.innerHTML = ''; resize(1024) })

describe('responsive usage details', () => {
  it.each(['token', 'cost'])('opens %s from the value button and survives synthetic touch hover/leave', async kind => {
    resize(390)
    const wrapper = render(false)
    const trigger = wrapper.get(`button[aria-label="usage.${kind === 'token' ? 'tokenDetails' : 'costDetails'}"]`)
    expect(trigger.text()).toContain(kind === 'token' ? '100' : '$0.120000')
    await trigger.trigger('mouseenter')
    await trigger.trigger('click')
    await trigger.trigger('mouseleave')
    await flushPromises()
    const overlay = document.body.querySelector('[role="dialog"]')!
    expect(overlay).not.toBeNull()
    expect(overlay.classList.contains('drawer-overlay')).toBe(false)
    expect(overlay.querySelector('.pointer-events-none')).toBeNull()
    expect(overlay.querySelector('.modal-body')?.textContent).toContain(kind === 'token' ? '100' : '0.12000000')
    expect(overlay.textContent).not.toContain('usage.accountBilled')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    await trigger.trigger('click'); await flushPromises()
    const reopened = document.body.querySelector('[role="dialog"]')!
    for (const type of ['mousedown', 'mouseup', 'click']) reopened.dispatchEvent(new MouseEvent(type, { bubbles: true }))
    await flushPromises()
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
  })
})
