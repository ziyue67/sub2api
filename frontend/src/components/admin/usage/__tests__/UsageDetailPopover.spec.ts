import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import UsageDetailPopover from '../UsageDetailPopover.vue'

afterEach(() => vi.restoreAllMocks())
it.each([{ left: 730, top: 8 }, { left: 10, top: 500 }])('positions the measured desktop panel at viewport edges ($left,$top)', async ({ left, top }) => {
  Object.defineProperty(window, 'innerWidth', { value: 768, configurable: true })
  Object.defineProperty(window, 'innerHeight', { value: 600, configurable: true })
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 360, height: 460 } as DOMRect)
  const wrapper = mount(UsageDetailPopover, {
    props: { show: true, title: 'Details', anchor: { left, right: left + 20, top, height: 20 } as DOMRect },
    slots: { default: '<p>Localized, long details</p>' },
    global: { stubs: { Teleport: true } },
  })
  try {
    await flushPromises()
    const panel = wrapper.get('[role="tooltip"]').element as HTMLElement
    const x = parseFloat(panel.style.left), y = parseFloat(panel.style.top)
    expect(x).toBeGreaterThanOrEqual(8)
    expect(x + 360).toBeLessThanOrEqual(760)
    expect(y).toBeGreaterThanOrEqual(8)
    expect(y + 460).toBeLessThanOrEqual(592)
    await wrapper.setProps({ anchor: { left: 400, right: 420, top: 290, height: 20 } as DOMRect })
    await flushPromises()
    expect(parseFloat((wrapper.get('[role="tooltip"]').element as HTMLElement).style.top)).toBe(70)
  } finally { wrapper.unmount() }
})
