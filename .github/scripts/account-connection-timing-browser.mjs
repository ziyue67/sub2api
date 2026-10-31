// Remote CI only. Real Vue modals; only captured synthetic handler SSE is used.
import { createRequire } from 'node:module'
import path from 'node:path'
import fs from 'node:fs/promises'
import { execFileSync } from 'node:child_process'

const root = process.cwd(), frontend = path.join(root, 'frontend')
const requireFrontend = createRequire(path.join(frontend, 'package.json'))
const { createServer } = requireFrontend('vite')
const vueModule = requireFrontend('@vitejs/plugin-vue'), vue = vueModule.default || vueModule
const tailwind = requireFrontend('tailwindcss'), autoprefixer = requireFrontend('autoprefixer')
const loadTailwindConfig = requireFrontend('tailwindcss/loadConfig')
const tailwindConfig = loadTailwindConfig(path.join(frontend, 'tailwind.config.js'))
const tailwindContent = Array.isArray(tailwindConfig.content) ? { files: tailwindConfig.content } : tailwindConfig.content
// Keep the real theme/plugins while making source discovery independent of cwd.
const browserTailwindConfig = {
  ...tailwindConfig,
  content: {
    ...tailwindContent,
    files: tailwindContent.files.map(file => typeof file === 'string' ? path.resolve(frontend, file) : file)
  }
}
const { chromium } = createRequire(path.join(process.env.PLAYWRIGHT_ROOT, 'package.json'))('playwright')
const output = process.env.ACCOUNT_TIMING_SCREENSHOT_DIR
await fs.mkdir(output, { recursive: true })
const fixture = `export const adminAPI={accounts:{getAvailableModels:async()=>[{id:'synthetic-model',display_name:'Synthetic model'}]}};`
const html = `<!doctype html><html><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><div id="app"></div><script type="module">
import {createApp,h,ref} from 'vue';import {createPinia} from 'pinia';import {createI18n} from 'vue-i18n';
import Account from '/src/components/account/AccountTestModal.vue';import Admin from '/src/components/admin/account/AccountTestModal.vue';
import en from '/src/i18n/locales/en/index.ts';import zh from '/src/i18n/locales/zh/index.ts';import '/src/style.css';
const params=new URLSearchParams(location.search),locale=params.get('locale')||'en';
const account={id:42,name:'Synthetic timing fixture',platform:'openai',type:'apikey',status:'active',credentials:{},extra:{},concurrency:1,priority:1,proxy_id:null};
createApp({setup(){const show=ref(false);return()=>h('div',[
h('button',{'data-testid':'open-timing-modal',onClick:()=>{show.value=true}},'Open timing fixture'),
h(params.get('variant')==='admin'?Admin:Account,{show:show.value,account,onClose:()=>{show.value=false}})
])}}).use(createPinia()).use(createI18n({legacy:false,locale,messages:{en,zh}})).mount('#app');</script></html>`

const server = await createServer({
  root: frontend, configFile: false,
  plugins: [vue(), {
    name: 'account-timing-synthetic',
    resolveId(id) { if (id === 'virtual:account-timing-api') return '\0' + id },
    load(id) { if (id === '\0virtual:account-timing-api') return fixture },
    configureServer(vite) {
      vite.middlewares.use((req, res, next) => {
        if (req.url?.split('?')[0] !== '/timing-fixture') return next()
        res.setHeader('Content-Type', 'text/html')
        void vite.transformIndexHtml('/timing-fixture', html).then(value => res.end(value))
      })
    }
  }],
  resolve: { alias: [{ find: /^@\/api\/admin$/, replacement: 'virtual:account-timing-api' }, { find: '@', replacement: path.join(frontend, 'src') }] },
  define: { __INTLIFY_JIT_COMPILATION__: true },
  css: { postcss: { plugins: [tailwind(browserTailwindConfig), autoprefixer()] } },
  server: { host: '127.0.0.1', port: 4191, strictPort: true },
  optimizeDeps: { noDiscovery: true, include: ['vue', 'vue-i18n', 'pinia'] }
})
await server.listen()
const browser = await chromium.launch({ headless: true })
const failures = [], observations = []
async function assertComputedStyles(page, label) {
  const css = await page.getByRole('dialog').evaluate(dialog => {
    const content = dialog.querySelector('.modal-content')
    const icon = dialog.querySelector('.modal-header svg.w-5.h-5')
    if (!content || !icon) throw new Error('Missing real dialog content or header icon')
    const overlayStyle = getComputedStyle(dialog), contentStyle = getComputedStyle(content), iconStyle = getComputedStyle(icon)
    return {
      overlayPosition: overlayStyle.position, overlayDisplay: overlayStyle.display,
      contentDisplay: contentStyle.display, contentDirection: contentStyle.flexDirection,
      iconWidth: iconStyle.width, iconHeight: iconStyle.height
    }
  })
  if (css.overlayPosition !== 'fixed' || css.overlayDisplay !== 'flex' ||
      css.contentDisplay !== 'flex' || css.contentDirection !== 'column' ||
      css.iconWidth !== '20px' || css.iconHeight !== '20px') {
    throw new Error(`${label}: invalid real dialog CSS ${JSON.stringify(css)}`)
  }
  return css
}
try {
  const sse = await fs.readFile(path.join(process.env.ACCOUNT_TIMING_FIXTURES_DIR, 'apikey-responses.sse'), 'utf8')
  for (const variant of ['account', 'admin']) for (const locale of ['en', 'zh']) {
    const context = await browser.newContext({ viewport: { width: 1360, height: 1000 }, timezoneId: 'Asia/Seoul' })
    const page = await context.newPage()
    page.on('pageerror', error => failures.push(error.message))
    await page.route('**/*', async route => {
      const url = new URL(route.request().url())
      if (url.pathname === '/api/v1/admin/accounts/42/test') return route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse })
      if (url.hostname === '127.0.0.1' && !url.pathname.startsWith('/api/')) return route.continue()
      failures.push(`Unexpected request: ${url.origin}${url.pathname}`)
      return route.abort()
    })
    await page.goto(`http://127.0.0.1:4191/timing-fixture?variant=${variant}&locale=${locale}`, { waitUntil: 'networkidle' })
    // Both real modals load models on the closed → open transition.
    await page.getByTestId('open-timing-modal').click()
    await page.getByText('Synthetic model', { exact: true }).waitFor()
    const start = page.getByRole('button', { name: locale === 'en' ? 'Start Test' : '开始测试', exact: true })
    await start.waitFor()
    await page.waitForFunction(element => element instanceof HTMLButtonElement && !element.disabled, await start.elementHandle())
    const desktopCss = await assertComputedStyles(page, `${variant}/${locale}/desktop`)
    await page.screenshot({ path: path.join(output, `${variant}-${locale}-before-start.png`), fullPage: true })
    await start.click()
    const panel = page.getByTestId('account-test-timing')
    await panel.getByText(locale === 'en' ? 'Server measurements received' : '已取得服务端计时', { exact: false }).waitFor()
    if (await panel.locator('details[open]').count()) throw new Error('details must be collapsed by default')
    if (!(await panel.textContent()).includes('UTC+09:00')) throw new Error('browser local timezone was not explicit')
    await page.screenshot({ path: path.join(output, `${variant}-${locale}-desktop.png`), fullPage: true })
    await page.setViewportSize({ width: 390, height: 844 })
    const mobileCss = await assertComputedStyles(page, `${variant}/${locale}/mobile`)
    await panel.scrollIntoViewIfNeeded()
    await page.screenshot({ path: path.join(output, `${variant}-${locale}-mobile.png`), fullPage: true })
    await panel.locator('summary').click()
    const overflow = await page.evaluate(() => {
      const panel = document.querySelector('[data-testid="account-test-timing"]')
      return { page: document.body.scrollWidth > innerWidth + 2, panel: panel.scrollWidth > panel.clientWidth + 2 }
    })
    if (overflow.page || overflow.panel) throw new Error(`${variant}/${locale}: mobile horizontal overflow`)
    await page.screenshot({ path: path.join(output, `${variant}-${locale}-mobile-details.png`), fullPage: true })
    observations.push({ variant, locale, openedFromClosed: true, modelLoaded: true, startEnabled: true, mobileWidth: 390, defaultCollapsed: true, timezone: 'Asia/Seoul UTC+09:00', css: { desktop: desktopCss, mobile: mobileCss }, overflow })
    await context.close()
  }
  if (failures.length) throw new Error(failures.join('; '))
  await fs.writeFile(path.join(output, 'verification.json'), JSON.stringify({
    exact_head: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(),
    source: 'captured real admin handler synthetic HTTP/SSE fixture', liveProviderCalls: 0, browserErrors: failures, observations
  }, null, 2))
} finally { await browser.close(); await server.close() }
