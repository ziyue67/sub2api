// CI only. Render the actual component with synthetic, offline API data.
import { createRequire } from 'node:module'
import path from 'node:path'
import fs from 'node:fs/promises'
const root = process.cwd(), frontend = path.join(root, 'frontend')
const requireFrontend = createRequire(path.join(frontend, 'package.json'))
const { createServer } = requireFrontend('vite')
const vueModule = requireFrontend('@vitejs/plugin-vue')
const vue = vueModule.default || vueModule
const requireBrowser = createRequire(path.join(process.env.PLAYWRIGHT_ROOT, 'package.json'))
const { chromium } = requireBrowser('playwright')
const tailwind = requireFrontend('tailwindcss')
const autoprefixer = requireFrontend('autoprefixer')
const output = process.env.QUALITY_SCREENSHOT_DIR
await fs.mkdir(output, { recursive: true })
const mockModule = `let reconciled=false;
const fixture=()=>({plan_id:7,account_id:42,rule_version:'synthetic-rule-v1',account_version:'synthetic-account-v1',fence:'a'.repeat(64),action:'isolate_models',models:['sample-astra'],group_ids:[101],relinquishable_count:reconciled?0:1,ownership_retained:true,restrictions_changed:false,entries:[
{kind:'model',model:'sample-astra',status:'managed',expected_present:true,current_present:true,expected_owner:'this_rule',current_owner:'this_rule',expected_digest:'a'.repeat(64),current_digest:'a'.repeat(64),changed_fields:[],can_relinquish:false},
...(reconciled?[]:[{kind:'model',model:'sample-sol',status:'missing',expected_present:true,current_present:false,expected_owner:'this_rule',current_owner:'absent',expected_digest:'b'.repeat(64),current_digest:'c'.repeat(64),changed_fields:['reason','isolated_at'],can_relinquish:true}])
]});export async function previewQualityOwnership(){return fixture()}export async function reconcileQualityOwnership(){reconciled=true;return fixture()}`
const html = `<!doctype html><meta charset="utf-8"><div id="app"></div><script type="module">
import {createApp,h} from 'vue';
import {createI18n} from 'vue-i18n';
import Review from '/src/components/admin/operations/QualityOwnershipReview.vue';
import zh from '/src/i18n/locales/zh/qualityOps.ts';
import '/src/style.css';
createApp({render:()=>h('main',{class:'mx-auto max-w-5xl space-y-4 p-6'},[h('h2',{class:'text-xl font-semibold'},'质量规则切换 — 虚构数据组件验收'),h('p','草稿尚未保存：sample-sol / 组102；规则名称是显示标签。'),h(Review,{planId:7,draftAction:'isolate_models',draftModels:['sample-sol'],draftGroupIds:[102]})])}).use(createI18n({legacy:false,locale:'zh',messages:{zh:{qualityOps:zh}}})).mount('#app');</script>`
process.chdir(frontend)
const server = await createServer({ root: frontend, configFile: false, plugins: [vue(), {
  name: 'quality-offline-fixture',
  resolveId(id) { if (id === 'virtual:quality-fixture') return '\0quality-fixture' },
  load(id) { if (id === '\0quality-fixture') return mockModule },
  configureServer(vite) { vite.middlewares.use((req, res, next) => { if (req.url === '/') { res.setHeader('Content-Type', 'text/html'); void vite.transformIndexHtml('/', html).then(value => res.end(value)); return } next() }) }
}], resolve: { alias: [{ find: '@/api/admin/accountQuality', replacement: 'virtual:quality-fixture' }, { find: '@', replacement: path.join(frontend, 'src') }] }, css: { postcss: { plugins: [tailwind({ config: path.join(frontend, 'tailwind.config.js') }), autoprefixer()] } }, server: { host: '127.0.0.1', port: 4179, strictPort: true }, optimizeDeps: { include: ['vue', 'vue-i18n'] } })
await server.listen()
const browser = await chromium.launch({ headless: true })
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 1000 } })
  const failures = []
  page.on('pageerror', error => failures.push(error.message))
  page.on('console', message => { if (message.type() === 'error') failures.push(message.text()) })
  await page.goto('http://127.0.0.1:4179/', { waitUntil: 'networkidle' })
  try { await page.getByTestId('quality-scope-conflict').waitFor() } catch {
    await page.screenshot({ path: path.join(output, 'render-failure-synthetic.png'), fullPage: true })
    await fs.writeFile(path.join(output, 'render-failure.json'), JSON.stringify({ errors: failures, syntheticBody: await page.locator('body').innerText() }, null, 2))
    throw new Error('synthetic_component_did_not_mount: ' + failures.join('; '))
  }
  if (failures.length) throw new Error('component_render_error')
  await page.screenshot({ path: path.join(output, 'scope-conflict-preview.png'), fullPage: true })
  await page.getByTestId('quality-ownership-confirm').check()
  await page.getByTestId('quality-ownership-reconcile').click()
  await page.getByRole('status').waitFor()
  await page.screenshot({ path: path.join(output, 'ownership-reconciled-retained-scope.png'), fullPage: true })
  await page.setViewportSize({ width: 440, height: 1000 })
  await page.screenshot({ path: path.join(output, 'retained-scope-mobile.png'), fullPage: true })
  await fs.writeFile(path.join(output, 'verification.json'), JSON.stringify({ commit: process.env.GITHUB_SHA, component: 'QualityOwnershipReview.vue', synthetic: true, liveApiCalls: 0, paidRequests: 0, errors: failures }, null, 2))
} finally { await browser.close(); await server.close() }
