import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  createPelicanPreviewDocument,
  fitPelicanArtwork,
  getPelicanViewport,
  readPelicanSizeMessage,
} from '../pelicanPreview'

describe('Pelican preview geometry', () => {
  it.each([
    [{ width: 1200, height: 600 }, { width: 400, height: 300 }, { scale: 1 / 3, width: 400, height: 200 }],
    [{ width: 600, height: 1200 }, { width: 400, height: 300 }, { scale: 0.25, width: 150, height: 300 }],
    [{ width: 800, height: 800 }, { width: 400, height: 300 }, { scale: 0.375, width: 300, height: 300 }],
  ])('contains the complete artwork without changing its proportions', (artwork, available, expected) => {
    expect(fitPelicanArtwork(artwork, available, 'fit')).toEqual(expected)
  })

  it('uses natural pixels only in explicit actual-size mode', () => {
    expect(fitPelicanArtwork({ width: 1200, height: 800 }, { width: 400, height: 300 }, 'actual'))
      .toEqual({ scale: 1, width: 1200, height: 800 })
  })

  it('does not enlarge small artwork or produce invalid geometry before layout', () => {
    expect(fitPelicanArtwork({ width: 100, height: 60 }, { width: 400, height: 300 }, 'fit'))
      .toEqual({ scale: 1, width: 100, height: 60 })
    const hidden = fitPelicanArtwork({ width: 1200, height: 800 }, { width: 0, height: 0 }, 'fit')
    expect(hidden.scale).toBe(0)
    expect(hidden.width).toBe(0)
  })

  it('uses a complete bare SVG viewBox as the logical viewport', () => {
    expect(getPelicanViewport('<svg viewBox="0 0 600 1200"><rect width="600" height="1200" /></svg>'))
      .toEqual({ width: 600, height: 1200 })
  })

  it('respects SVG pixel dimensions and retains a stable viewport for HTML wrappers', () => {
    expect(getPelicanViewport('<svg width="800" height="400" viewBox="0 0 1600 800"></svg>'))
      .toEqual({ width: 800, height: 400 })
    expect(getPelicanViewport('<html><body><h1>Original title</h1><svg viewBox="0 0 600 1200"></svg></body></html>'))
      .toEqual({ width: 1024, height: 768 })
  })

  it('falls back for invalid or unreasonably large dimensions', () => {
    expect(getPelicanViewport('<svg width="0" height="-1" viewBox="0 0 NaN 0"></svg>'))
      .toEqual({ width: 1024, height: 768 })
    expect(getPelicanViewport('<svg width="999999" height="999999"></svg>'))
      .toEqual({ width: 1024, height: 768 })
  })
})

describe('Pelican opaque sandbox messages', () => {
  const source = {} as Window
  const valid = { type: 'pelican-preview:size', channel: 'one-preview', width: 1600, height: 1200 }

  it('accepts bounded sizes only from its own iframe and current channel', () => {
    const event = { source, data: valid } as MessageEvent
    expect(readPelicanSizeMessage(event, source, 'one-preview')).toEqual({ width: 1600, height: 1200 })
    expect(readPelicanSizeMessage({ ...event, source: {} } as MessageEvent, source, 'one-preview')).toBeNull()
    expect(readPelicanSizeMessage(event, source, 'reloaded-preview')).toBeNull()
  })

  it.each([null, {}, { ...valid, width: '1600' }, { ...valid, width: Infinity }, { ...valid, height: -1 }, { ...valid, width: 8193 }])('ignores malformed messages without changing dimensions', (data) => {
    expect(readPelicanSizeMessage({ source, data } as MessageEvent, source, 'one-preview')).toBeNull()
  })
})

describe('Pelican document instrumentation', () => {
  it('preserves original HTML, SVG, title, and scripts behind the existing restrictive CSP', () => {
    const original = '<html><head><title>Original title</title></head><body><h1>Caption</h1><svg viewBox="0 0 1200 600"><animate attributeName="x" /></svg><script>window.artworkAnimation = true</script></body></html>'
    const out = createPelicanPreviewDocument(original, 'preview-123')
    const doc = new DOMParser().parseFromString(out, 'text/html')
    expect(doc.head.firstElementChild?.getAttribute('http-equiv')).toBe('Content-Security-Policy')
    expect(doc.head.firstElementChild?.getAttribute('content')).toContain("connect-src 'none'")
    expect(doc.head.firstElementChild?.getAttribute('content')).toContain("frame-src 'none'")
    expect(doc.title).toBe('Original title')
    expect(doc.querySelector('h1')?.textContent).toBe('Caption')
    expect(doc.querySelector('animate')).not.toBeNull()
    expect(Array.from(doc.scripts).some((script) => script.textContent === 'window.artworkAnimation = true')).toBe(true)
    expect(doc.querySelector('script[data-pelican-preview="preview-123"]')).not.toBeNull()
  })
})

describe('Pelican sandbox measurement runtime', () => {
  let iframe: HTMLIFrameElement | undefined
  let sandbox: Window & typeof globalThis
  const reports: Array<{ width: number; height: number }> = []
  let contentWidth = 1600
  let contentHeight = 1200

  function start(html: string, initialWidth = 1600, initialHeight = 1200, artBounds?: DOMRect, prepare?: (frame: Window & typeof globalThis) => void) {
    vi.useFakeTimers()
    reports.length = 0
    contentWidth = initialWidth
    contentHeight = initialHeight
    iframe = document.createElement('iframe')
    document.body.append(iframe)
    sandbox = iframe.contentWindow as Window & typeof globalThis
    sandbox.document.open()
    sandbox.document.write(createPelicanPreviewDocument(html, 'runtime-preview'))
    sandbox.document.close()
    Object.defineProperties(sandbox.document.documentElement, {
      scrollWidth: { configurable: true, get: () => contentWidth },
      scrollHeight: { configurable: true, get: () => contentHeight },
    })
    Object.defineProperties(sandbox.document.body, {
      scrollWidth: { configurable: true, get: () => contentWidth },
      scrollHeight: { configurable: true, get: () => contentHeight },
    })
    if (artBounds) sandbox.document.getElementById('art')!.getBoundingClientRect = () => artBounds
    // jsdom exposes a distinct parent WindowProxy for this iframe. Capture the
    // sandbox-to-host boundary directly; document measurement still runs for real.
    Object.defineProperty(sandbox, 'parent', { configurable: true, value: { postMessage: (message: { width: number; height: number }) => reports.push(message) } })
    prepare?.(sandbox)
    const script = sandbox.document.querySelector('script[data-pelican-preview]')!.textContent!
    new Function('window', script)(sandbox)
    sandbox.document.dispatchEvent(new sandbox.Event('DOMContentLoaded'))
  }

  afterEach(() => {
    sandbox?.dispatchEvent(new sandbox.Event('pagehide'))
    iframe?.remove()
    iframe = undefined
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('reports the full oversized document and follows delayed content without reloading it', async () => {
    start('<html><body><h1>Original title</h1><div id="art" style="min-width:1600px">Full HTML artwork</div></body></html>')
    expect(reports.at(-1)).toMatchObject({ width: 1600, height: 1200 })
    contentHeight = 1800
    sandbox.document.getElementById('art')!.append('Delayed detail')
    await Promise.resolve()
    await vi.advanceTimersByTimeAsync(100)
    expect(reports.at(-1)).toMatchObject({ width: 1600, height: 1800 })
    expect(sandbox.document.querySelector('h1')?.textContent).toBe('Original title')
  })

  it('fits overflow inside the fixed native viewport without treating its unscaled scrollbar floor as content', async () => {
    start('<html><body><div>Portrait artwork</div></body></html>', 1024, 1600)
    const root = sandbox.document.documentElement
    expect(root.style.zoom).toBe('0.48')
    Object.defineProperties(root, {
      scrollWidth: { configurable: true, value: 1024 },
      scrollHeight: { configurable: true, value: 768 },
    })
    await vi.advanceTimersByTimeAsync(6000)
    expect(reports).toHaveLength(1)
    expect(reports[0]).toMatchObject({ width: 1024, height: 1600 })
    expect(root.style.zoom).toBe('0.48')
  })

  it('preserves the author root zoom while applying its own reduction', () => {
    // jsdom does not parse the zoom declaration; provide the browser's property.
    start('<html style="zoom:2"><body>Artwork</body></html>', 1600, 1200, undefined, (frame) => { frame.document.documentElement.style.zoom = '2' })
    expect(reports.at(-1)).toMatchObject({ width: 3200, height: 2400 })
    expect(sandbox.document.documentElement.style.zoom).toBe('0.64')
  })

  it('includes a centered fixed-size artwork that initially extends beyond the top and left edges', () => {
    start('<html><body><div id="art">Artwork</div></body></html>', 1024, 768, { left: -288, top: -216, right: 1312, bottom: 984, width: 1600, height: 1200 } as DOMRect)
    expect(reports.at(-1)).toMatchObject({ width: 1600, height: 1200 })
  })

  it('keeps a fixed marker stationary when another element animates from negative to positive coordinates', async () => {
    let movingX = -40
    let movingY = -20
    start('<html><body><div id="marker">Fixed marker</div><div id="art">Moving artwork</div></body></html>', 1024, 768, undefined, (frame) => {
      const root = frame.document.documentElement
      const rect = (x: number, y: number, width: number, height: number) => {
        const shifts = Array.from(root.style.translate.matchAll(/\+ (\d+)px/g), (match) => Number(match[1]))
        const left = x + (shifts[0] || 0)
        const top = y + (shifts[1] || 0)
        return { left, top, right: left + width, bottom: top + height, width, height } as DOMRect
      }
      root.getBoundingClientRect = () => rect(0, 0, 1024, 768)
      frame.document.body.getBoundingClientRect = () => rect(0, 0, 1024, 768)
      frame.document.getElementById('marker')!.getBoundingClientRect = () => rect(0, 0, 20, 20)
      frame.document.getElementById('art')!.getBoundingClientRect = () => rect(movingX, movingY, 200, 200)
    })
    const marker = sandbox.document.getElementById('marker')!
    expect(marker.getBoundingClientRect()).toMatchObject({ left: 40, top: 20 })
    movingX = 40
    movingY = 20
    sandbox.document.getElementById('art')!.style.transform = 'translate(80px, 40px)'
    await Promise.resolve()
    await vi.advanceTimersByTimeAsync(500)
    expect(marker.getBoundingClientRect()).toMatchObject({ left: 40, top: 20 })
    expect(reports.every((report) => report.width <= 8192 && report.height <= 8192)).toBe(true)
  })

  it('pins viewport-relative sizing to its logical canvas so growing the iframe cannot create a size loop', async () => {
    start('<html><head><style>body{min-height:100vh}.art{width:80vw;animation:float 2s infinite}@keyframes float{to{transform:translateY(10vh)}}</style></head><body><div class="art">Artwork</div></body></html>')
    const sheet = sandbox.document.styleSheets[0]
    expect((sheet.cssRules[0] as CSSStyleRule).style.getPropertyValue('min-height')).toBe('768px')
    expect((sheet.cssRules[1] as CSSStyleRule).style.getPropertyValue('width')).toBe('819.2px')
    Object.defineProperty(sandbox, 'innerHeight', { configurable: true, value: 1200 })
    sandbox.dispatchEvent(new sandbox.Event('resize'))
    await vi.advanceTimersByTimeAsync(1000)
    expect(reports).toHaveLength(1)
    expect((sheet.cssRules[0] as CSSStyleRule).style.getPropertyValue('min-height')).toBe('768px')
    const late = sandbox.document.createElement('div')
    late.style.minHeight = '200vh'
    sandbox.document.body.append(late)
    await Promise.resolve()
    await vi.advanceTimersByTimeAsync(100)
    expect(late.style.minHeight).toBe('1536px')
  })

  it('keeps authored text and data URLs unchanged while pinning CSS lengths', () => {
    start('<html><head><style>.art::before{content:"100vh"}.art{background-image:url("data:image/svg+xml,100vw");height:100vh}</style></head><body><div class="art">Artwork</div></body></html>')
    const rules = sandbox.document.styleSheets[0].cssRules
    expect((rules[0] as CSSStyleRule).style.getPropertyValue('content')).toBe('"100vh"')
    expect((rules[1] as CSSStyleRule).style.getPropertyValue('background-image')).toContain('100vw')
    expect((rules[1] as CSSStyleRule).style.getPropertyValue('height')).toBe('768px')
  })

  it('does not grow percentage-height documents again when a footer expands the outer iframe', async () => {
    start('<html><head><style>html,body{height:100%}</style></head><body><main style="height:100%">Artwork</main><footer>Caption</footer></body></html>', 1024, 832)
    Object.defineProperty(sandbox.document.documentElement, 'scrollHeight', { configurable: true, get: () => (parseFloat(sandbox.document.documentElement.style.height) || sandbox.innerHeight) + 64 })
    Object.defineProperty(sandbox, 'innerHeight', { configurable: true, value: 832 })
    sandbox.dispatchEvent(new sandbox.Event('resize'))
    await vi.advanceTimersByTimeAsync(1000)
    expect(reports.at(-1)).toMatchObject({ width: 1024, height: 832 })
    expect(reports).toHaveLength(1)
  })

  it('bounds oversized output and stops observers and timers when the iframe leaves', async () => {
    start('<html><body>Artwork</body></html>')
    contentWidth = 999999
    contentHeight = 999999
    sandbox.document.body.append('More')
    await Promise.resolve()
    await vi.advanceTimersByTimeAsync(100)
    expect(reports.at(-1)).toMatchObject({ width: 8192, height: 8192 })
    sandbox.dispatchEvent(new sandbox.Event('pagehide'))
    const count = reports.length
    sandbox.document.body.append('After removal')
    await vi.advanceTimersByTimeAsync(6000)
    expect(reports).toHaveLength(count)
  })
})
