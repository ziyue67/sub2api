import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import PelicanArtworkPreview from '../PelicanArtworkPreview.vue'

let wrapper: ReturnType<typeof mount> | undefined
afterEach(() => { wrapper?.unmount(); wrapper = undefined; vi.restoreAllMocks() })

function mountPreview(mode: 'fit' | 'actual' = 'fit', ancestorScale = 1) {
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(400)
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(300)
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 400 * ancestorScale, height: 300 * ancestorScale } as DOMRect)
  wrapper = mount(PelicanArtworkPreview, { attachTo: document.body, props: { html: '<svg viewBox="0 0 1200 600"></svg>', title: 'Artwork', mode } })
  return wrapper
}

describe('PelicanArtworkPreview', () => {
  it('fits the logical client area even when the dialog enters with an ancestor scale transform', async () => {
    const preview = mountPreview('fit', 0.95)
    await preview.vm.$nextTick()
    expect(preview.get('iframe').element.style.transform).toBe('scale(0.3333333333333333)')
  })

  it('contains the logical canvas and keeps the sandbox opaque', async () => {
    const preview = mountPreview()
    await preview.vm.$nextTick()
    const frame = preview.get('iframe')
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(frame.attributes('referrerpolicy')).toBe('no-referrer')
    expect(frame.element.style.width).toBe('1200px')
    expect(frame.element.style.height).toBe('600px')
    expect(frame.element.style.transform).toBe('scale(0.3333333333333333)')
    expect(preview.element.style.overflow).toBe('clip')
  })

  it('fits delayed content while preserving the native viewport, srcdoc, and animation', async () => {
    const preview = mountPreview()
    const frame = preview.get('iframe')
    const srcdoc = frame.attributes('srcdoc')
    const channel = srcdoc.match(/data-pelican-preview="([^"]+)"/)![1]
    window.dispatchEvent(new MessageEvent('message', { source: frame.element.contentWindow, data: { type: 'pelican-preview:size', channel, width: 1600, height: 1200 } }))
    await preview.vm.$nextTick()
    expect(frame.element.style.width).toBe('1200px')
    expect(frame.element.style.height).toBe('600px')
    // The sandbox fits 1600x1200 into its fixed 1200x600 viewport at 0.5.
    // Outer 0.5 * inner 0.5 = 0.25, yielding the intended 400x300 preview.
    expect(frame.element.style.transform).toBe('scale(0.5)')
    expect(frame.attributes('srcdoc')).toBe(srcdoc)
    await preview.setProps({ mode: 'actual' })
    // At 100%, the outer transform cancels the sandbox's inner reduction.
    expect(frame.element.style.transform).toBe('scale(2)')
    expect(frame.element.style.width).toBe('1200px')
    expect(frame.element.style.height).toBe('600px')
  })

  it('ignores another iframe and stale measurements after the artwork changes', async () => {
    const preview = mountPreview()
    const frame = preview.get('iframe')
    const channel = frame.attributes('srcdoc').match(/data-pelican-preview="([^"]+)"/)![1]
    window.dispatchEvent(new MessageEvent('message', { source: window, data: { type: 'pelican-preview:size', channel, width: 1600, height: 1200 } }))
    await preview.vm.$nextTick()
    expect(frame.element.style.width).toBe('1200px')
    await preview.setProps({ html: '<svg viewBox="0 0 600 1200"></svg>' })
    window.dispatchEvent(new MessageEvent('message', { source: frame.element.contentWindow, data: { type: 'pelican-preview:size', channel, width: 1600, height: 1200 } }))
    await preview.vm.$nextTick()
    expect(frame.element.style.width).toBe('600px')
    expect(frame.element.style.height).toBe('1200px')
  })

  it('switches to intentional natural-size scrolling without restarting the document', async () => {
    const preview = mountPreview()
    const srcdoc = preview.get('iframe').attributes('srcdoc')
    await preview.setProps({ mode: 'actual' })
    expect(preview.element.style.overflow).toBe('auto')
    expect(preview.get('iframe').element.style.transform).toBe('scale(1)')
    expect(preview.get('iframe').attributes('srcdoc')).toBe(srcdoc)
    await preview.setProps({ interactive: false })
    expect(preview.get('iframe').attributes('tabindex')).toBe('-1')
    expect(preview.get('iframe').element.style.pointerEvents).toBe('none')
  })
})
