// Remote CI only: actual usage page and network component, synthetic API adapter.
import { createRequire } from 'node:module'
import path from 'node:path'
import fs from 'node:fs/promises'
import { execFileSync } from 'node:child_process'

const root = process.env.VIEW_ROOT || process.cwd()
const before = process.env.SCREENSHOT_MODE === 'before'
const frontend = path.join(root, 'frontend')
const requireFrontend = createRequire(path.join(frontend, 'package.json'))
const { createServer } = requireFrontend('vite')
const vueModule = requireFrontend('@vitejs/plugin-vue')
const tailwind = requireFrontend('tailwindcss'), autoprefixer = requireFrontend('autoprefixer')
const { chromium } = createRequire(path.join(process.env.PLAYWRIGHT_ROOT, 'package.json'))('playwright')
const output = process.env.SCREENSHOT_DIR
await fs.mkdir(output, { recursive: true })
const fixture = `
import { apiClient } from '/src/api/client.ts'
const group={account_id:1,model:'synthetic-model',reasoning_samples:12,text_samples:12,byte_samples:12,reasoning_first_avg_ms:250,visible_text_first_avg_ms:6050,response_byte_first_avg_ms:80,visible_text_p50_ms:5500,visible_text_p90_ms:7000,visible_text_p99_ms:9000,body_bucket:'1-4MiB',cache_bucket:'95%+',reasoning_effort:'xhigh',requests:12,traces:12,attempts:15,upload_samples:14,wait_samples:11,visible_samples:11,body_upload_avg_ms:1550,post_upload_first_content_wait_avg_ms:3200,post_upload_first_visible_wait_avg_ms:3500,large_body_switches:2,large_body_same_account_retries:1,failover_samples:12,cost_samples:2,extra_upload_component_ms:3300,extra_wait_component_ms:2100}
const row={content_timing:{reasoning_first_ms:250,visible_text_first_ms:6050,response_byte_first_ms:80,status:'observed',source:'httptrace_and_typed_sse',scope:'final_http',reasoning_streaming_capability:'unknown',capability_source:'none'},id:603,user_id:1,api_key_id:1,account_id:1,request_id:'synthetic-request-603',model:'synthetic-model',requested_model:'synthetic-model',input_tokens:1000,output_tokens:400,cache_read_tokens:19000,cache_creation_tokens:0,input_cost:0,output_cost:0,cache_creation_cost:0,cache_read_cost:0,total_cost:0,actual_cost:0,rate_multiplier:1,billing_type:0,billing_mode:'token',stream:true,request_type:2,first_token_ms:6050,duration_ms:15000,created_at:'2026-10-30T00:00:00Z',reasoning_effort:'xhigh',user:{id:1,email:'synthetic@test.invalid',username:'synthetic'},api_key:{id:1,name:'Synthetic key'},account:{id:1,name:'Synthetic account'}}
const stats={total_requests:12,total_tokens:20400,total_input_tokens:1000,total_output_tokens:400,total_cache_tokens:19000,total_cache_read_tokens:19000,total_cache_creation_tokens:0,total_cost:0,total_actual_cost:0,average_duration_ms:15000,endpoints:[],upstream_endpoints:[],endpoint_paths:[]}
const minutes=Array.from({length:20},(_,i)=>({at:new Date(Date.UTC(2026,9,30,0,i)).toISOString(),instance:'00000000-0000-4000-8000-000000000603',egress_mbps:i===8?null:2+(i%7),tcp_retrans_percent:i===0?null:(i%4)/10,qdisc_drops:i===0?null:i%3,egress_status:i===8?'counter_reset_or_topology_change':'measured',tcp_status:i===0?'initial_sample':'measured',qdisc_status:i===0?'initial_sample':'measured_root_egress_qdisc',window_seconds:60}))
globalThis.__issue603Calls=[]
apiClient.defaults.adapter=async request=>{
 const url=request.url;globalThis.__issue603Calls.push(url);let data=[]
 if(url.endsWith('/latency-report'))data={groups:[group],end_to_end_ms:null,end_to_end_status:'not_collected',end_to_end_source:'none',client_upload_ms:null,client_upload_status:'not_collected'}
 else if(url.endsWith('/network-series'))data={points:minutes,body_points:minutes.map((p,i)=>({at:p.at,bytes:1048576*(1+i%5),samples:1+i%3})),scope:'synthetic'}
 else if(url.endsWith('/usage/stats'))data=stats
 else if(url.endsWith('/usage'))data={items:[row],total:1,page:1,page_size:20,pages:1}
 else if(url.endsWith('/snapshot-v2'))data={stats,trend:[],models:[],groups:[]}
 else if(url.endsWith('/models'))data={models:[]}
 else if(url.endsWith('/settings')||url.endsWith('/settings/public'))data={run_mode:'simple',ops_monitoring_enabled:true}
 else if(url.endsWith('/accounts')||url.endsWith('/groups'))data={items:[],total:0,pages:0}
 else if(url.endsWith('/groups/all')||url.endsWith('/api-keys')||url.includes('/search-'))data=[]
 return {data:{code:0,message:'success',data},status:200,statusText:'OK',headers:{},config:request}
}
`
const shell = `import { h } from 'vue'; export default {setup(_,ctx){return()=>h('main',{class:'mx-auto max-w-7xl p-4'},ctx.slots.default?.())}}`
const html = `<!doctype html><html><meta charset="utf-8"><div id="app"></div><script type="module">
import 'virtual:issue603-fixture';import {createApp,h} from 'vue';import {createPinia} from 'pinia';import {createRouter,createWebHistory,RouterView} from 'vue-router';import {createI18n} from 'vue-i18n';import {useAuthStore} from '/src/stores/auth.ts';import UsageView from '/src/views/admin/UsageView.vue';${before ? '' : "import Network from '/src/views/admin/ops/components/OpsNetworkChart.vue';"}import en from '/src/i18n/locales/en/index.ts';import zh from '/src/i18n/locales/zh/index.ts';import '/src/style.css';
const pinia=createPinia(),router=createRouter({history:createWebHistory(),routes:[{path:'/admin/usage',component:UsageView}${before ? '' : ",{path:'/network',component:{render:()=>h('main',{class:'p-8'},[h(Network)])}}"}]});const app=createApp(RouterView);app.use(pinia);useAuthStore().user={id:1,email:'synthetic@test.invalid',role:'admin',status:'active',balance:0,concurrency:1};app.use(router).use(createI18n({legacy:false,locale:new URL(location.href).searchParams.get('lang')||'en',messages:{en,zh}})).mount('#app')</script></html>`
process.chdir(frontend)
const server = await createServer({ root: frontend, configFile: false, plugins: [(vueModule.default || vueModule)(), {
  name: 'issue603-offline-fixture',
  resolveId(id) { if (id === 'virtual:issue603-fixture' || id === 'virtual:issue603-shell') return `\0${id}` },
  load(id) { if (id === '\0virtual:issue603-fixture') return fixture; if (id === '\0virtual:issue603-shell') return shell },
  configureServer(vite) { vite.middlewares.use((req, res, next) => {
    if (['/admin/usage','/network'].includes(req.url?.split('?')[0])) { res.setHeader('Content-Type','text/html'); void vite.transformIndexHtml(req.url,html).then(value=>res.end(value)); return }
    next()
  }) }
}], resolve: { alias: [{find:'@/components/layout/AppLayout.vue',replacement:'virtual:issue603-shell'},{find:'@',replacement:path.join(frontend,'src')}] }, css: { postcss: { plugins: [tailwind({config:path.join(frontend,'tailwind.config.js')}),autoprefixer()] } }, server:{host:'127.0.0.1',port:4193,strictPort:true}, optimizeDeps:{noDiscovery:true,include:['vue','vue-i18n','pinia','vue-router','axios','@vueuse/core','@tanstack/vue-virtual','chart.js','vue-chartjs','dompurify','qrcode','driver.js','file-saver']} })
await server.listen()
const browser = await chromium.launch({headless:true})
const page = await browser.newPage({viewport:{width:1440,height:1100}})
const errors=[],blocked=[]
page.on('pageerror',err=>errors.push(err.message))
await page.route('**/*',route=>{const url=new URL(route.request().url());if(url.hostname!=='127.0.0.1'){blocked.push(url.hostname);return route.abort()}return route.continue()})
try {
  for (const lang of ['en','zh']) {
    await page.goto(`http://127.0.0.1:4193/admin/usage?lang=${lang}`,{waitUntil:'networkidle'})
    await page.getByText('synthetic-model',{exact:true}).first().waitFor()
    if (!before) { await page.getByTestId('latency-observability').waitFor();await page.getByTestId('latency-group').waitFor();if(!(await page.getByTestId('latency-e2e').innerText()).includes(lang==='en'?'Source: none':'来源：无'))throw new Error('missing explicit E2E source') }
    await page.screenshot({path:path.join(output,`${before?'before':'after'}-usage-${lang}-desktop.png`),fullPage:true})
    await page.setViewportSize({width:390,height:844})
    await page.screenshot({path:path.join(output,`${before?'before':'after'}-usage-${lang}-mobile.png`),fullPage:true})
    await page.setViewportSize({width:1440,height:1100})
  }
  if (!before) {
    await page.goto('http://127.0.0.1:4193/network',{waitUntil:'networkidle'});await page.getByTestId('ops-network-chart').waitFor()
    if(!(await page.getByTestId('network-qdisc').innerText()).includes('measured_root_egress_qdisc'))throw new Error('root qdisc measurement state lost')
    await page.screenshot({path:path.join(output,'after-network-desktop.png'),fullPage:true})
    await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(output,'after-network-mobile.png'),fullPage:true})
  }
  if(errors.length||blocked.length)throw new Error(JSON.stringify({errors,blocked}))
  await fs.writeFile(path.join(output,`${before?'before':'after'}-verification.json`),JSON.stringify({head:execFileSync('git',['rev-parse','HEAD'],{cwd:root,encoding:'utf8'}).trim(),synthetic:true,browserErrors:errors,externalRequests:blocked,calls:await page.evaluate(()=>globalThis.__issue603Calls)},null,2))
} catch(err) {
  await page.screenshot({path:path.join(output,`${before?'before':'after'}-failure.png`),fullPage:true})
  await fs.writeFile(path.join(output,`${before?'before':'after'}-failure.txt`),String(err)+'\n'+JSON.stringify({errors,blocked})+'\n'+await page.locator('body').innerText())
  throw err
} finally {await browser.close();await server.close()}
