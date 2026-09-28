import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import PelicanShowcaseRow from '../PelicanShowcaseRow.vue'

// jsdom has no layout: report the sizes a browser would, per element.
function setWidths(el: HTMLElement, sizes: { clientWidth: number; scrollWidth: number }) {
  Object.defineProperty(el, 'clientWidth', { configurable: true, get: () => sizes.clientWidth })
  Object.defineProperty(el, 'scrollWidth', { configurable: true, get: () => sizes.scrollWidth })
}

function setRect(el: Element, left: number, width: number) {
  vi.spyOn(el, 'getBoundingClientRect').mockReturnValue({
    left, width, right: left + width, top: 0, bottom: 6, height: 6, x: left, y: 0, toJSON: () => ({}),
  } as DOMRect)
}

// jsdom has no PointerEvent; the handlers only read these fields.
function pointer(type: string, clientX: number) {
  const event = new MouseEvent(type, { bubbles: true, button: 0, clientX })
  Object.defineProperty(event, 'pointerId', { value: 7 })
  return event
}

const count = ref(2)
const Host = defineComponent({
  setup: () => () => h(PelicanShowcaseRow, { label: 'Claude Max' }, {
    default: () => Array.from({ length: count.value }, (_, i) => h('div', { key: i, class: 'card' })),
  }),
})

let wrapper: ReturnType<typeof mount>
afterEach(() => {
  wrapper?.unmount()
  count.value = 2
  vi.restoreAllMocks()
})

function mountRow(sizes: { clientWidth: number; scrollWidth: number }) {
  wrapper = mount(Host)
  const row = wrapper.get('[data-testid="pelican-showcase-row"]').element as HTMLElement
  setWidths(row, sizes)
  row.dispatchEvent(new Event('scroll'))
  return {
    row,
    slider: wrapper.get('[data-testid="pelican-showcase-slider"]'),
    thumb: () => wrapper.get('[data-testid="pelican-showcase-slider-thumb"]'),
  }
}

describe('PelicanShowcaseRow', () => {
  it('shows no slider while every card fits', async () => {
    const { slider } = mountRow({ clientWidth: 800, scrollWidth: 801 })
    await flushPromises()
    expect(wrapper.findAll('.card')).toHaveLength(2)
    expect(slider.attributes('style')).toContain('display: none')
  })

  it('sizes the thumb by the visible share and moves it with the row', async () => {
    const { row, slider, thumb } = mountRow({ clientWidth: 400, scrollWidth: 1600 })
    row.scrollLeft = 600
    row.dispatchEvent(new Event('scroll'))
    await flushPromises()

    expect(slider.attributes('style') ?? '').not.toContain('display: none')
    expect(slider.attributes('aria-controls')).toBe(row.id)
    expect(slider.attributes('aria-label')).toBe('Claude Max')
    expect(slider.attributes('aria-valuenow')).toBe('50')
    const style = thumb().attributes('style')
    expect(style).toContain('width: 25%')
    expect(style).toContain('left: 50%')
    expect(style).toContain('translateX(-50%)')
  })

  it('scrolls the row while the thumb is dragged, and jumps to where the track is pressed', async () => {
    const { row, slider, thumb } = mountRow({ clientWidth: 400, scrollWidth: 1600 })
    const track = thumb().element.parentElement!
    // 300px of thumb travel covers 1200px of scrolling: 4px per pixel.
    setRect(track, 0, 400)
    setRect(thumb().element, 0, 100)

    slider.element.dispatchEvent(pointer('pointerdown', 50))
    await flushPromises()
    expect(row.scrollLeft).toBe(0)
    expect(thumb().classes()).toContain('bg-primary-500')
    slider.element.dispatchEvent(pointer('pointermove', 110))
    await flushPromises()
    expect(row.scrollLeft).toBe(240)
    expect(slider.attributes('aria-valuenow')).toBe('20')
    slider.element.dispatchEvent(pointer('pointerup', 110))
    slider.element.dispatchEvent(pointer('pointermove', 300))
    await flushPromises()
    expect(row.scrollLeft).toBe(240)
    expect(thumb().classes()).not.toContain('bg-primary-500')

    // The thumb now spans 60–160px; pressing at 350px centres it there, clamped to the end.
    setRect(thumb().element, 60, 100)
    slider.element.dispatchEvent(pointer('pointerdown', 350))
    await flushPromises()
    expect(row.scrollLeft).toBe(1200)
    expect(slider.attributes('aria-valuenow')).toBe('100')
  })

  it('moves by one card with the arrow keys and to either end with Home / End', async () => {
    const { row, slider } = mountRow({ clientWidth: 400, scrollWidth: 1600 })
    Object.defineProperty(row.firstElementChild!, 'offsetWidth', { configurable: true, value: 300 })
    const scrollTo = vi.fn()
    row.scrollTo = scrollTo as typeof row.scrollTo
    row.scrollLeft = 100

    await slider.trigger('keydown', { key: 'ArrowRight' })
    expect(scrollTo).toHaveBeenLastCalledWith(expect.objectContaining({ left: 400 }))
    await slider.trigger('keydown', { key: 'ArrowLeft' })
    expect(scrollTo).toHaveBeenLastCalledWith(expect.objectContaining({ left: -200 }))
    await slider.trigger('keydown', { key: 'End' })
    expect(scrollTo).toHaveBeenLastCalledWith(expect.objectContaining({ left: 1600 }))
    await slider.trigger('keydown', { key: 'Home' })
    expect(scrollTo).toHaveBeenLastCalledWith(expect.objectContaining({ left: 0 }))
    await slider.trigger('keydown', { key: 'Enter' })
    expect(scrollTo).toHaveBeenCalledTimes(4)
  })

  it('brings the slider in once added cards overflow the row', async () => {
    const sizes = { clientWidth: 800, scrollWidth: 800 }
    const { slider } = mountRow(sizes)
    await flushPromises()
    expect(slider.attributes('style')).toContain('display: none')

    // Cards change the scroll width without resizing the row, so only the child list reports it.
    sizes.scrollWidth = 1600
    count.value = 5
    await flushPromises()
    expect(wrapper.findAll('.card')).toHaveLength(5)
    expect(slider.attributes('style') ?? '').not.toContain('display: none')
  })
})
