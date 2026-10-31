// Remote isolated runner only. Real usage components and locales, synthetic API.
import { createRequire } from 'node:module'
import path from 'node:path'
import fs from 'node:fs/promises'
import { execFileSync } from 'node:child_process'
const root = process.env.VIEW_ROOT || process.cwd(), frontend = path.join(root, 'frontend')
const before = process.env.SCREENSHOT_MODE === 'before', output = process.env.SCREENSHOT_DIR
const requireFrontend = createRequire(path.join(frontend, 'package.json'))
const { createServer } = requireFrontend('vite'), vue = requireFrontend('@vitejs/plugin-vue')
const tailwind = requireFrontend('tailwindcss'), autoprefixer = requireFrontend('autoprefixer')
const { chromium } = createRequire(path.join(process.env.PLAYWRIGHT_ROOT, 'package.json'))('playwright')
await fs.mkdir(output, { recursive: true })
const fixture = `
import {apiClient} from '/src/api/client.ts'
apiClient.defaults.adapter=async config=>({data:{code:0,message:'success',data:{traces:[],retention_days:30}},status:200,statusText:'OK',headers:{},config})
const base={id:610,user_id:1,api_key_id:1,account_id:1,request_id:'synthetic-610',model:'synthetic-ctx',requested_model:'synthetic-ctx',input_tokens:1000,output_tokens:100,cache_read_tokens:0,cache_creation_tokens:0,total_cost:0,actual_cost:0,rate_multiplier:1,billing_type:0,billing_mode:'token',stream:true,request_type:3,first_token_ms:2000,duration_ms:20000,queue_wait_ms:30000,created_at:'2026-10-30T00:00:00Z',reasoning_effort:'xhigh',content_timing:{reasoning_first_ms:250,visible_text_first_ms:2000,response_byte_first_ms:80,status:'observed',source:'httptrace_and_typed_sse',scope:'final_http',reasoning_streaming_capability:'unknown',capability_source:'none'},queue_timing:{status:'measured',source:'ws_frame_ctx_pool',scope:'pre_final_forward',server_first_token_ms:32000,server_duration_ms:50000,forward_first_token_ms:2000,forward_duration_ms:20000,queue_clamped:false}}
export const rows=[base,{...base,id:611,request_id:'synthetic-passthrough',model:'synthetic-pass',requested_model:'synthetic-pass',first_token_ms:32000,duration_ms:50000,queue_timing:{...base.queue_timing,source:'ws_frame_passthrough',scope:'accepted_frame_including_admission'}}]
`
const html = `<!doctype html><html><meta charset="utf-8"><div id="app"></div><script type="module">
import {rows} from 'virtual:pr610-fixture';import {createApp,h} from 'vue';import {createPinia} from 'pinia';import {createI18n} from 'vue-i18n';import Table from '/src/components/admin/usage/UsageTable.vue';import en from '/src/i18n/locales/en/index.ts';import zh from '/src/i18n/locales/zh/index.ts';import '/src/style.css';
createApp({render:()=>h('main',{class:'mx-auto max-w-6xl p-8'},[h('h1',{class:'mb-4 text-xl'},'PR610 · synthetic usage'),h(Table,{data:rows,loading:false,columns:[{key:'model',label:'Model'},{key:'latency',label:'Timing'}],enableTimingDetails:true})])}).use(createPinia()).use(createI18n({legacy:false,locale:new URL(location.href).searchParams.get('lang')||'en',messages:{en,zh}})).mount('#app')
</script></html>`
process.chdir(frontend)
const server = await createServer({root:frontend,configFile:false,plugins:[(vue.default||vue)(),{
  name:'pr610-fixture', resolveId(id){if(id==='virtual:pr610-fixture')return '\0'+id},load(id){if(id==='\0virtual:pr610-fixture')return fixture},
  configureServer(vite){vite.middlewares.use((req,res,next)=>{if(req.url?.startsWith('/queue-fixture')){res.setHeader('Content-Type','text/html');void vite.transformIndexHtml(req.url,html).then(value=>res.end(value));return}next()})}
}],resolve:{alias:{'@':path.join(frontend,'src')}},css:{postcss:{plugins:[tailwind({config:path.join(frontend,'tailwind.config.js')}),autoprefixer()]}},server:{host:'127.0.0.1',port:4194,strictPort:true},optimizeDeps:{noDiscovery:true,include:['vue','vue-i18n','pinia','vue-router','axios','@vueuse/core','@tanstack/vue-virtual','dompurify','qrcode','driver.js','file-saver']}})
await server.listen()
const browser=await chromium.launch({headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000}})
const errors=[],external=[],interactions=[]
page.on('pageerror',error=>errors.push(error.message))
await page.route('**/*',route=>{const url=new URL(route.request().url());if(url.hostname!=='127.0.0.1'){external.push(url.hostname);return route.abort()}return route.continue()})
try {
 for(const lang of ['en','zh']){
  await page.goto(`http://127.0.0.1:4194/queue-fixture?lang=${lang}`,{waitUntil:'networkidle'})
  await page.getByText('synthetic-pass',{exact:true}).first().waitFor()
  const pass=page.locator('tbody tr').nth(1)
  const text=await pass.innerText()
  if(!text.includes(before?'1m 2s':'32.00s'))throw new Error('passthrough overlap assertion: '+text)
  if(!before){
   if(text.includes('1m 2s'))throw new Error('queue added twice')
   if(await page.getByTestId('typed-content-timing').count()!==2)throw new Error('typed content columns lost')
   if(!(await page.getByTestId('latency-e2e-uncollected').first().getAttribute('title'))?.includes(lang==='en'?'none':'无'))throw new Error('client E2E source must remain none')
  }
  await page.screenshot({path:path.join(output,`${before?'before':'after'}-table-${lang}-desktop.png`),fullPage:true,animations:'disabled'})
  await pass.getByRole('button',{name:lang==='en'?'Request timing details':'请求耗时详情'}).click()
  const dialog=page.getByRole('dialog');await dialog.waitFor()
  // Vue drawer transitions can expose an accessible dialog while its panel is
  // still outside the viewport. Finish animations before the review artifact.
  await page.evaluate(async()=>{await Promise.all(document.getAnimations().filter(a=>a.playState==='running').map(a=>a.finished.catch(()=>{})))})
  const bounds=await dialog.boundingBox()
  if(!bounds||bounds.x<0||bounds.x+bounds.width>1441)throw new Error('dialog did not settle inside desktop viewport')
  const body=await dialog.innerText()
  if(!before && ((body.includes('62.00s')||body.includes('1m 2s'))||!body.includes(lang==='en'?'Server TTFT (including pre-forward wait)':'服务端首字（含转发前等待）')))throw new Error('dialog server timing label/value lost')
  await page.screenshot({path:path.join(output,`${before?'before':'after'}-dialog-${lang}-desktop.png`),fullPage:true,animations:'disabled'})
  await page.setViewportSize({width:390,height:844})
  await page.screenshot({path:path.join(output,`${before?'before':'after'}-dialog-${lang}-mobile.png`),fullPage:true,animations:'disabled'})
  await page.keyboard.press('Escape');await dialog.waitFor({state:'hidden'})
  await page.screenshot({path:path.join(output,`${before?'before':'after'}-table-${lang}-mobile.png`),fullPage:true,animations:'disabled'})
  interactions.push({lang,passthroughText:text,dialogOpened:true,escapeClosed:true,desktopAndMobile:true})
  await page.setViewportSize({width:1440,height:1000})
 }
 if(errors.length||external.length)throw new Error(JSON.stringify({errors,external}))
 await fs.writeFile(path.join(output,`${before?'before':'after'}-verification.json`),JSON.stringify({head:execFileSync('git',['rev-parse','HEAD'],{cwd:root,encoding:'utf8'}).trim(),synthetic:true,browserErrors:errors,externalRequests:external,interactions},null,2))
} catch(error){
 await page.screenshot({path:path.join(output,`${before?'before':'after'}-failure.png`),fullPage:true,animations:'disabled'})
 await fs.writeFile(path.join(output,`${before?'before':'after'}-failure.txt`),String(error)+'\n'+JSON.stringify({errors,external})+'\n'+await page.locator('body').innerText());throw error
} finally{await browser.close();await server.close()}
