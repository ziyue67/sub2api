import { afterEach, describe, expect, it } from 'vitest'
import { authorizePelicanInlineScripts, extractPelicanHtml, readPelicanPreviewNonce } from '../pelicanHtml'
import { createPelicanPreviewDocument } from '../pelicanPreview'

const nonce = 'pageNonce+123/abc=='
const parse = (html: string) => new DOMParser().parseFromString(html, 'text/html')

describe('Pelican inherited CSP nonce', () => {
  afterEach(() => document.head.querySelectorAll('script[data-nonce-test]').forEach((node) => node.remove()))

  it('reads the trusted page script nonce property', () => {
    const script = document.createElement('script')
    script.dataset.nonceTest = ''
    script.nonce = nonce
    document.head.append(script)
    expect(readPelicanPreviewNonce()).toBe(nonce)
  })

  it('authorizes inline artwork and instrumentation without changing original source', () => {
    const raw = '<html lang="zh"><head><title>Pelican</title></head><body><svg viewBox="0 0 10 20"><path id="leg"/><animateTransform attributeName="transform"/></svg><script nonce="old">document.getElementById("leg").setAttribute("d","M0 0 L1 1")</script></body></html>'
    const out = parse(createPelicanPreviewDocument(raw, 'preview-one', nonce))
    expect(Array.from(out.scripts)).toHaveLength(2)
    expect(Array.from(out.scripts).every((script) => script.nonce === nonce)).toBe(true)
    expect(out.querySelector('svg')?.getAttribute('viewBox')).toBe('0 0 10 20')
    expect(out.querySelector('animateTransform')).not.toBeNull()
    expect(raw).toContain('nonce="old"')
    expect(out.title).toBe('Pelican')
    const csp = out.head.firstElementChild?.getAttribute('content')
    expect(csp).toContain("script-src 'unsafe-inline'")
    expect(csp).not.toContain(nonce)
    for (const directive of ['connect-src', 'frame-src', 'object-src', 'form-action']) {
      expect(csp).toContain(`${directive} 'none'`)
    }
  })

  it('does not authorize external HTML or SVG scripts even with supplied author nonces', () => {
    const raw = '<html><body><script src="https://evil.test/x.js" nonce="old"></script><svg><script href="https://evil.test/y.js" nonce="old"/><script xlink:href="https://evil.test/z.js" nonce="old"/></svg></body></html>'
    const out = parse(authorizePelicanInlineScripts(extractPelicanHtml(raw), nonce))
    expect(out.querySelectorAll('script')).toHaveLength(3)
    expect(out.querySelectorAll('script[nonce]')).toHaveLength(0)
  })

  it.each(['', '\" onload=alert(1)', 'bad<nonce'])('does not inject missing or invalid nonces: %s', (value) => {
    const out = parse(createPelicanPreviewDocument('<html><body><script nonce="old">window.test = true</script></body></html>', 'safe-channel', value))
    expect(out.querySelectorAll('script[nonce]')).toHaveLength(0)
    expect(out.querySelectorAll('script[onload]')).toHaveLength(0)
  })

  it('preserves restrictive CSP when extraction is applied repeatedly', () => {
    const raw = '<html><body><script>window.test = true</script></body></html>'
    const out = parse(createPelicanPreviewDocument(extractPelicanHtml(raw), 'preview-two', nonce))
    expect(out.head.firstElementChild?.getAttribute('content')).toContain("connect-src 'none'")
    expect(Array.from(out.scripts).every((script) => script.nonce === nonce)).toBe(true)
  })
})
