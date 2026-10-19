// Browser fixture: real UsageTable/BaseDialog/styles, synthetic records, no backend.
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { resolve } from 'node:path'
import assert from 'node:assert/strict'
const require = createRequire(import.meta.url)
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')
const root = fileURLToPath(new URL('..', import.meta.url))
const fixture = `import { createApp, h } from 'vue';
import { createPinia } from 'pinia';
import UsageTable from '/src/components/admin/usage/UsageTable.vue';
import { i18n, loadLocaleMessages } from '/src/i18n/index.ts';
import '/src/style.css';
await loadLocaleMessages('zh'); i18n.global.locale.value = 'zh';
const row = { id: 1, request_id: 'fixture-only', model: 'fixture-model', created_at: '2026-10-18',
input_tokens: 123456, output_tokens: 3456, cache_read_tokens: 10000, cache_creation_tokens: 5000,
cache_creation_5m_tokens: 3000, cache_creation_1h_tokens: 2000, cache_ttl_overridden: true,
image_input_tokens: 1000, image_output_tokens: 2000, image_input_cost: 0.1, image_output_cost: 0.2,
input_cost: 0.12, output_cost: 0.34, cache_creation_cost: 0.05, cache_read_cost: 0.01,
total_cost: 0.82, actual_cost: 0.41, rate_multiplier: 0.5, account_rate_multiplier: 2,
duration_ms: 3000, first_token_ms: 1000 };
createApp({ render: () => h(UsageTable, { data: [row], enableTimingDetails: true,
columns: [{ key: 'tokens', label: 'Token' }, { key: 'cost', label: '费用' }, { key: 'latency', label: '延迟' }] }) })
.use(createPinia()).use(i18n).mount('#app');`
const server = await createServer({
  root, configFile: false,
  plugins: [vue(), {
    name: 'usage-browser-fixture',
    configureServer(server) {
      server.middlewares.use('/usage-fixture', (_req, res) => { res.setHeader('Content-Type', 'text/html'); res.end('<div id="app"></div><script type="module" src="/fixture-entry.js"></script>') })
    },
    resolveId(id) { if (id === '/fixture-entry.js' || id === 'fixture-router') return '\0' + id },
    load(id) {
      if (id === '\0/fixture-entry.js') return fixture
      if (id === '\0fixture-router') return 'export default { push() {}, currentRoute: { value: { path: \'/fixture\' } } }'
    },
  }],
  resolve: { alias: [{ find: /^@\/router$/, replacement: 'fixture-router' }, { find: '@', replacement: resolve(root, 'src') }] },
  server: { host: '127.0.0.1', port: 0 },
})
let browser
try {
  await server.listen()
  const address = server.httpServer.address()
  browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] })
  const results = []
  for (const width of [320, 390, 768, 1280]) {
    const page = await browser.newPage({ viewport: { width, height: 240 }, hasTouch: width < 768 })
    const errors = []
    page.on('pageerror', error => { errors.push(error.message); console.error('PAGE ERROR', error.stack) })
    page.on('console', msg => { if (msg.type() === 'error') console.error('CONSOLE', msg.text()) })
    await page.route(/\/api\/v1\/admin\/usage\/1\/timing(?:\?|$)/, route => route.fulfill({ json: { code: 0, data: { traces: [], retention_days: 30 } } }))
    await page.goto(`http://127.0.0.1:${address.port}/usage-fixture`)
    await page.getByRole('button', { name: 'Token 明细', exact: true }).first().waitFor({ timeout: 5000 })
    for (const name of ['Token 明细', '费用明细']) {
      const trigger = page.getByRole('button', { name, exact: true }).first()
      if (width < 768) await trigger.tap()
      else await trigger.hover()
      const panel = width < 768 ? page.locator('.modal-content') : page.getByRole('tooltip')
      await panel.waitFor()
      await page.waitForTimeout(350)
      const bounds = await panel.boundingBox()
      assert(bounds.x >= 0 && bounds.x + bounds.width <= width + 1, JSON.stringify({ width, name, bounds }))
      assert(bounds.y >= 0 && bounds.y + bounds.height <= 241, JSON.stringify({ width, name, bounds }))
      assert(await panel.evaluate(el => el.scrollWidth <= el.clientWidth + 1), `horizontal overflow: ${width} ${name}`)
      if (width < 768) {
        assert(Math.abs(bounds.x + bounds.width / 2 - width / 2) < 2, 'not centered')
        const body = page.locator('.modal-body')
        const scrolled = await body.evaluate(el => { el.scrollTop = el.scrollHeight; return { top: el.scrollTop, height: el.scrollHeight, client: el.clientHeight } })
        assert(scrolled.top > 0, `mobile body not scrollable: ${JSON.stringify(scrolled)}`)
        await page.keyboard.press('Escape')
        await page.getByRole('dialog').waitFor({ state: 'detached' })
        await trigger.tap()
        await page.getByRole('dialog').waitFor()
        await page.locator('.modal-overlay').click({ position: { x: 1, y: 1 } })
        await page.getByRole('dialog').waitFor({ state: 'detached' })
      } else {
        await panel.hover()
        await page.mouse.wheel(0, 300)
        assert(await panel.isVisible(), 'popover closed while interacting')
        await page.mouse.move(width - 1, 239)
        await panel.waitFor({ state: 'detached' })
      }
      results.push({ width, detail: name, bounds, scrolling: true })
    }
    await page.getByRole('button', { name: '请求耗时详情', exact: true }).first().click()
    const timing = page.locator('.modal-overlay')
    await timing.waitFor()
    assert.equal(await timing.evaluate(el => el.classList.contains('drawer-overlay')), width >= 768)
    await page.setViewportSize({ width: width < 768 ? 1024 : 390, height: 240 })
    await page.waitForTimeout(100)
    assert.equal(await timing.evaluate(el => el.classList.contains('drawer-overlay')), width < 768)
    assert.deepEqual(errors, [])
    await page.close()
  }
  console.log(JSON.stringify({ fixture: true, passed: results.length, results }, null, 2))
} finally { await browser?.close(); await server.close() }
