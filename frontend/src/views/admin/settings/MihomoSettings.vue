<template>
  <div class="space-y-5" :aria-busy="busy">
    <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-700">
      <div class="space-y-1">
        <p class="text-sm font-medium" role="status">Mihomo <span class="badge ml-2" :class="status?.running ? 'badge-success' : 'badge-gray'">{{ status?.phase || '—' }}</span></p>
        <p class="text-xs text-gray-500">{{ text('内核本地出口', 'Local endpoint') }} <code>{{ status?.endpoint || '—' }}</code> · {{ status?.nodes || 0 }} {{ text('个节点', 'nodes') }}</p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="refresh">{{ text('检测状态', 'Check status') }}</button>
    </div>
    <div v-if="warmPools.length" class="grid gap-3 sm:grid-cols-2">
      <div v-for="pool in warmPools" :key="pool.source" class="rounded-xl border border-gray-200 p-3 text-xs dark:border-dark-600" data-testid="bps-warm-pool">
        <p class="font-medium">{{ pool.source }} · Warm IP pool</p>
        <p class="mt-2">{{ text('就绪', 'Ready') }} {{ pool.ready }} / {{ text('目标', 'Target') }} {{ pool.target }} · {{ text('探测中', 'Checking') }} {{ pool.checking }} · {{ text('冷却中', 'Cooling') }} {{ pool.cooling }}</p>
        <p v-if="pool.source === 'Mihomo' && pool.ready_subscription !== undefined" class="mt-1">{{ text('订阅就绪', 'Ready subscription exits') }} {{ pool.ready_subscription }} · {{ text('动态就绪', 'Ready dynamic exits') }} {{ pool.ready_dynamic || 0 }}</p>
        <p class="mt-1 text-gray-500">{{ text('目标按已启用账号的并发数汇总；不足时复用就绪出口，用户请求不探测冷节点。', 'Targets follow enabled account concurrency. Ready exits are reused when scarce; requests never probe cold nodes.') }}</p>
        <p v-for="(count, reason) in pool.failure_reasons" :key="reason">{{ reason }}: {{ count }}</p>
        <p v-if="pool.target > 0 && pool.ready === 0" class="mt-1 text-amber-700">{{ text('后台正在预热；不会退回直连。', 'Background warming continues; direct fallback is disabled.') }}</p>
      </div>
    </div>
    <p v-if="error || status?.error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20">{{ error || status?.error }}</p>
    <p v-if="success" role="status" class="text-sm text-green-700 dark:text-green-400">{{ success }}</p>
    <p v-if="busy" role="status" class="text-sm text-primary-600">{{ text('正在应用，请等待完成…', 'Applying changes. Please wait…') }}</p>

    <template v-if="section === 'subscriptions'">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="font-semibold">{{ text('Mihomo 订阅', 'Mihomo subscriptions') }}</h2><p class="mt-1 text-xs text-gray-500">{{ text('按订阅管理来源；地址和令牌不回显。添加订阅不会替换其他来源。', 'Manage each source independently. URLs and tokens stay hidden; adding a subscription preserves other sources.') }}</p></div>
        <button type="button" class="btn btn-primary" :disabled="busy || !status?.installed" @click="openSubscription()">{{ text('添加订阅', 'Add subscription') }}</button>
      </div>
      <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-600">
        <div class="flex flex-wrap items-center gap-3">
          <label for="subscription-download-mode" class="text-sm font-medium">{{ text('订阅下载模式', 'Subscription download mode') }}</label>
          <select id="subscription-download-mode" v-model="downloadMode" class="input w-auto" :disabled="busy" @change="downloadModeDirty = true">
            <option value="auto">{{ text('自动：代理失败后直连', 'Auto: proxy with direct fallback') }}</option>
            <option value="proxy">{{ text('仅通过 Mihomo', 'Mihomo proxy only') }}</option>
            <option value="direct">{{ text('仅直连', 'Direct only') }}</option>
          </select>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !downloadModeDirty" @click="saveDownloadMode">{{ text('保存下载模式', 'Save download mode') }}</button>
        </div>
        <p class="mt-2 text-xs text-gray-500">{{ text('仅影响订阅文件下载，不改变打票或账号业务出口。自动模式在代理下载失败或内容无效时尝试直连；仅代理模式不会回退直连。', 'Only affects subscription downloads, not ticket or account traffic. Auto retries direct after a failed or invalid proxy response; proxy-only never falls back to direct.') }}</p>
      </div>
      <div class="flex flex-wrap gap-3">
        <input v-model="search" type="search" class="input sm:max-w-xs" :placeholder="text('搜索订阅名称', 'Search subscription names')" :aria-label="text('搜索订阅', 'Search subscriptions')" />
        <div v-if="selected.length" class="flex flex-wrap items-center gap-2 text-sm">
          <span>{{ text('已选', 'Selected') }} {{ selected.length }}</span>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="batchSubscriptions('subscription_refresh')">{{ text('更新所选', 'Refresh selected') }}</button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="batchSubscriptions('subscription_enable')">{{ text('启用所选', 'Enable selected') }}</button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="batchSubscriptions('subscription_disable')">{{ text('停用所选', 'Disable selected') }}</button>
          <button type="button" class="btn btn-danger btn-sm" :disabled="busy" @click="confirmRemoval(selected.map(id => 'subscription_remove/' + id))">{{ text('移除所选', 'Remove selected') }}</button>
        </div>
      </div>
      <DataTable v-model:selected-keys="selected" :columns="subscriptionColumns" :data="filteredSubscriptions" row-key="id" selectable :loading="loading">
        <template #cell-label="{ row }"><span class="font-medium">{{ row.label }}</span><code class="ml-2 text-xs text-gray-400">{{ row.id.slice(0, 8) }}</code></template>
        <template #cell-enabled="{ row }"><span class="badge" :class="row.enabled ? 'badge-success' : 'badge-gray'">{{ row.enabled ? text('启用', 'Enabled') : text('停用', 'Disabled') }}</span></template>
        <template #cell-nodes="{ row }">{{ row.cached ? row.nodes : text('待更新', 'Not refreshed') }}</template>
        <template #cell-updated_at="{ row }">{{ row.updated_at ? new Date(row.updated_at).toLocaleString() : '—' }}</template>
        <template #cell-actions="{ row }">
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !row.enabled" @click="operate('subscription_refresh/' + row.id)">{{ text('更新', 'Refresh') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="openSubscription(row)">{{ text('编辑', 'Edit') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="operate((row.enabled ? 'subscription_disable/' : 'subscription_enable/') + row.id)">{{ row.enabled ? text('停用', 'Disable') : text('启用', 'Enable') }}</button>
            <button type="button" class="btn btn-danger btn-sm" :disabled="busy" @click="confirmRemoval(['subscription_remove/' + row.id])">{{ text('移除', 'Remove') }}</button>
          </div>
        </template>
        <template #empty><p class="p-8 text-center text-sm text-gray-500">{{ text('暂无匹配的订阅。点击“添加订阅”导入 Clash / Mihomo YAML 来源。', 'No matching subscriptions. Add a Clash / Mihomo YAML source to get started.') }}</p></template>
      </DataTable>
      <p v-if="status?.subscriptions && !status.subscription_items" class="text-sm text-amber-700">{{ text('当前后端尚不支持订阅列表，请升级后端后使用逐条管理。', 'This backend does not support subscription lists yet. Upgrade it to manage individual sources.') }}</p>
    </template>

    <template v-if="section === 'dynamic' || section === 'nodes'">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="font-semibold">{{ section === 'dynamic' ? text('动态代理', 'Dynamic proxies') : text('节点管理', 'Node management') }}</h2><p class="mt-1 text-xs text-gray-500">{{ text('检测仅测试网络连接，不调用模型。订阅节点从所属订阅移除；动态代理可单独删除。', 'Checks test connectivity without model calls. Remove subscription nodes through their source; dynamic proxies can be deleted individually.') }}</p></div>
        <button v-if="section === 'dynamic'" type="button" class="btn btn-primary" :disabled="busy || !status?.installed" @click="dynamicDialog = true">{{ text('导入动态代理', 'Import dynamic proxies') }}</button>
      </div>
      <div class="flex flex-wrap gap-3">
        <input v-model="search" type="search" class="input sm:max-w-xs" :placeholder="text('搜索节点名称或地区', 'Search node names or regions')" :aria-label="text('搜索节点', 'Search nodes')" />
        <select v-model="nodeState" class="input w-auto" :aria-label="text('节点状态', 'Node status')">
          <option value="">{{ text('全部状态', 'All statuses') }}</option><option v-for="state in nodeStates" :key="state" :value="state">{{ stateLabel(state) }}</option>
        </select>
        <select v-if="section === 'nodes'" v-model="nodeSource" class="input w-auto" :aria-label="text('节点来源', 'Node source')">
          <option value="">{{ text('全部来源', 'All sources') }}</option><option value="dynamic">{{ text('动态代理', 'Dynamic proxies') }}</option>
          <option v-for="source in status?.subscription_items || []" :key="source.id" :value="source.id">{{ source.label }}</option>
        </select>
      </div>
      <DataTable :columns="nodeColumns" :data="pagedNodes" row-key="name" :loading="loading">
        <template #cell-display_name="{ row }">{{ row.display_name || row.name }}</template>
        <template #cell-source="{ row }">{{ nodeSourceLabel(row) }}</template>
        <template #cell-state="{ row }"><span class="badge" :class="row.state === 'enabled' ? 'badge-success' : 'badge-gray'">{{ stateLabel(row.state) }}</span></template>
        <template #cell-country_code="{ row }">{{ row.dynamic ? text('供应商控制', 'Provider managed') : row.country_code || text('未知', 'Unknown') }}</template>
        <template #cell-actions="{ row }">
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !status?.running" @click="operate('probe/' + row.name)">{{ text('检测', 'Test') }}</button>
            <button v-if="!row.dynamic" type="button" class="btn btn-secondary btn-sm" :disabled="busy || !status?.running" @click="operate('country_probe/' + row.name)">{{ text('检测地区', 'Check region') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || row.state === 'country_excluded'" @click="operate((row.state === 'enabled' ? 'disable/' : 'recover/') + row.name)">{{ row.state === 'enabled' ? text('停用', 'Disable') : text('恢复', 'Recover') }}</button>
            <button v-if="row.dynamic" type="button" class="btn btn-danger btn-sm" :disabled="busy" @click="confirmRemoval(['dynamic_remove/' + row.name])">{{ text('移除', 'Remove') }}</button>
          </div>
        </template>
      </DataTable>
      <div class="flex items-center justify-between text-sm">
        <span>{{ text('共', 'Total') }} {{ filteredNodes.length }} {{ text('个节点', 'nodes') }} · {{ page }} / {{ totalPages }}</span>
        <div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="page <= 1" @click="page--">{{ text('上一页', 'Previous') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="page >= totalPages" @click="page++">{{ text('下一页', 'Next') }}</button></div>
      </div>
    </template>

    <template v-if="section === 'kernel'">
      <h2 class="font-semibold">{{ text('内核与地区规则', 'Kernel and region rules') }}</h2>
      <div class="flex flex-wrap gap-2">
        <button v-if="status && !status.installed" type="button" class="btn btn-primary" :disabled="busy || !status.supported" @click="operate('install')">{{ text('检测并安装', 'Install kernel') }}</button>
        <button v-else-if="!status?.running" type="button" class="btn btn-secondary" :disabled="busy || !status?.nodes" @click="operate('start')">{{ text('启动内核', 'Start kernel') }}</button>
      </div>
      <label v-if="status?.installed" class="flex items-center gap-2 text-sm"><input type="checkbox" :checked="status.use_once" :disabled="busy" @change="operate(status.use_once ? 'once_off' : 'once_on')" />{{ text('打票节点用后移出（需手动恢复）', 'Retire harvest nodes after use (manual recovery)') }}</label>
      <MihomoCountryFilter v-if="status?.installed" :filter="status.country_filter" :codes="status.country_codes || []" :nodes="status.node_states || []" :busy="busy" @save="operate('country_filter', { country_filter: $event })" @scan="operate('country_scan')" />
      <p class="text-xs text-gray-500">{{ text('这里的配置独立保存。打票代理在系统设置中选择，账号业务代理在账号设置中选择。', 'Configuration is saved here independently. Select ticket proxies in system settings and account proxies in account settings.') }}</p>
    </template>
    <p v-if="status && !status.installed && section !== 'kernel'" class="text-sm text-amber-700">{{ text('请先在“内核与规则”中安装内核。', 'Install the kernel in Kernel and rules first.') }}</p>

    <BaseDialog :show="subscriptionDialog" :title="editing ? text('编辑订阅', 'Edit subscription') : text('添加订阅', 'Add subscription')" @close="subscriptionDialog = false">
      <div class="space-y-4">
        <label class="block text-sm">{{ text('订阅名称（单条订阅，可选）', 'Subscription name (single source, optional)') }}<input v-model="subscriptionName" maxlength="80" class="input mt-2" data-testid="subscription-name" /></label>
        <label class="block text-sm" for="mihomo-subscriptions">{{ editing ? text('新订阅地址（留空只修改名称）', 'New URL (leave blank to rename only)') : text('机场订阅地址（每行一个）', 'Subscription URLs (one per line)') }}</label>
        <textarea id="mihomo-subscriptions" v-model="subscriptions" class="input w-full font-mono text-xs" rows="5" autocomplete="off" spellcheck="false" />
        <p class="text-xs text-gray-500">{{ text('支持 Clash / Mihomo YAML。地址含密钥时请勿填入名称；已保存的地址不会回显。', 'Requires Clash / Mihomo YAML. Keep secrets out of names; saved URLs are never displayed.') }}</p>
        <input v-if="!editing" type="file" accept=".txt,text/plain" :aria-label="text('导入订阅 TXT', 'Import subscription TXT')" @change="importFile" />
        <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      </div>
      <template #footer><button type="button" class="btn btn-primary" :disabled="busy || (!subscriptions.trim() && !editing)" @click="saveSubscription">{{ text('保存并应用', 'Save and apply') }}</button></template>
    </BaseDialog>
    <BaseDialog :show="dynamicDialog" :title="text('导入动态代理', 'Import dynamic proxies')" @close="dynamicDialog = false">
      <div class="space-y-3">
        <label class="block text-sm" for="mihomo-dynamic-protocol">{{ text('无协议前缀时使用', 'Default protocol') }}</label>
        <select id="mihomo-dynamic-protocol" v-model="dynamicProtocol" class="input"><option value="http">HTTP</option><option value="https">HTTPS</option><option value="socks5">SOCKS5</option><option value="socks5h">SOCKS5H</option></select>
        <label class="block text-sm" for="mihomo-dynamic-proxies">{{ text('动态代理（每行一个）', 'Dynamic proxies (one per line)') }}</label>
        <textarea id="mihomo-dynamic-proxies" v-model="dynamicProxies" class="input w-full font-mono text-xs" rows="6" autocomplete="off" spellcheck="false" />
        <details class="text-xs text-gray-500"><summary>{{ text('支持的输入格式', 'Supported formats') }}</summary><pre class="mt-2 whitespace-pre-wrap">hostname:port:username:password
username:password:hostname:port
username:password@hostname:port
hostname:port@username:password
http://username:password@hostname:port</pre></details>
        <label class="flex items-center gap-2 text-sm"><input v-model="replaceDynamic" type="checkbox" />{{ text('替换已保存的动态代理（不勾选则追加）', 'Replace saved dynamic proxies (otherwise append)') }}</label>
        <p class="text-xs text-gray-500">{{ text('不会删除机场订阅。更换出口的国家／地区仍由供应商控制。', 'Subscriptions are preserved. Exit regions remain controlled by the provider.') }}</p>
        <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      </div>
      <template #footer><button type="button" class="btn btn-primary" :disabled="busy || !dynamicProxies.trim()" @click="saveDynamic">{{ text('应用动态代理', 'Apply dynamic proxies') }}</button></template>
    </BaseDialog>
    <ConfirmDialog :show="!!confirmation" :title="text('确认变更代理来源', 'Confirm proxy source changes')" :message="confirmation?.message || ''" danger @cancel="confirmation = undefined" @confirm="confirmActions" />
  </div>
</template>
<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { apiClient } from '@/api/client'
import DataTable from '@/components/common/DataTable.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import MihomoCountryFilter from './MihomoCountryFilter.vue'
import type { CountryFilter, CountryNode } from './mihomoCountry'
const props = withDefaults(defineProps<{ section?: 'subscriptions' | 'dynamic' | 'nodes' | 'kernel' }>(), { section: 'subscriptions' })
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
type SubscriptionDownloadMode = 'auto' | 'proxy' | 'direct'
interface WarmPoolStatus { ready_subscription?: number; ready_dynamic?: number; target: number; ready: number; checking: number; cooling: number; failure_reasons?: Record<string, number> }
interface Subscription { id: string; label: string; enabled: boolean; nodes: number; cached: boolean; updated_at?: string }
interface ManagedNode extends CountryNode { subscription_ids?: string[] }
interface Status { subscription_download_mode?: SubscriptionDownloadMode; bps_warm_pool?: WarmPoolStatus; bps_ip_warm_pool?: WarmPoolStatus; installed: boolean; running: boolean; busy: boolean; supported: boolean; phase: string; error?: string; nodes: number; subscriptions: number; subscription_items?: Subscription[]; dynamic_proxies?: number; endpoint: string; use_once?: boolean; node_states?: ManagedNode[]; country_filter?: CountryFilter; country_codes?: string[] }
interface Payload { subscriptions?: string[]; dynamic_proxies?: string[]; country_filter?: CountryFilter; name?: string }
const status = ref<Status>()
const loading = ref(true)
const pending = ref(false)
const busy = computed(() => pending.value || !!status.value?.busy)
const error = ref('')
const success = ref('')
const subscriptions = ref('')
const subscriptionName = ref('')
const subscriptionDialog = ref(false)
const downloadMode = ref<SubscriptionDownloadMode>('auto')
const downloadModeDirty = ref(false)
watch(() => status.value?.subscription_download_mode, mode => { if (!downloadModeDirty.value) downloadMode.value = mode || 'auto' })
const editing = ref<Subscription>()
const dynamicDialog = ref(false)
const dynamicProxies = ref('')
const dynamicProtocol = ref('http')
const replaceDynamic = ref(false)
const selected = ref<Array<string | number>>([])
const search = ref('')
const nodeState = ref('')
const nodeSource = ref('')
const page = ref(1)
const nodeStates = ['enabled', 'disabled', 'failed', 'used', 'country_excluded']
const confirmation = ref<{ actions: string[]; message: string; payload?: Payload }>()
const stateLabel = (state: string) => ({ enabled: text('启用', 'Enabled'), disabled: text('停用', 'Disabled'), failed: text('检测失败', 'Failed'), used: text('已使用', 'Used'), country_excluded: text('地区已排除', 'Region excluded') }[state] || state)
const warmPools = computed(() => [
  { source: 'Mihomo', pool: status.value?.bps_warm_pool },
  { source: text('IP 管理', 'IP Management'), pool: status.value?.bps_ip_warm_pool }
].flatMap(item => item.pool ? [{ source: item.source, ...item.pool }] : []))
const subscriptionColumns = computed(() => [
  { key: 'label', label: text('名称', 'Name'), sortable: true }, { key: 'enabled', label: text('状态', 'Status') },
  { key: 'nodes', label: text('节点数', 'Nodes') }, { key: 'updated_at', label: text('最后更新', 'Last refreshed') }, { key: 'actions', label: text('操作', 'Actions') }
])
const nodeColumns = computed(() => [
  { key: 'display_name', label: text('名称', 'Name') }, { key: 'source', label: text('来源', 'Source') },
  { key: 'state', label: text('状态', 'Status') }, { key: 'country_code', label: text('地区', 'Region') }, { key: 'actions', label: text('操作', 'Actions') }
])
const filteredSubscriptions = computed(() => (status.value?.subscription_items || []).filter(s => (s.label + s.id).toLowerCase().includes(search.value.trim().toLowerCase())))
const filteredNodes = computed(() => (status.value?.node_states || []).filter(n =>
  (props.section !== 'dynamic' || n.dynamic) && (!nodeState.value || n.state === nodeState.value) &&
  (!nodeSource.value || (nodeSource.value === 'dynamic' ? n.dynamic : n.subscription_ids?.includes(nodeSource.value))) &&
  ((n.display_name || n.name) + ' ' + (n.country_code || '')).toLowerCase().includes(search.value.trim().toLowerCase())
))
const totalPages = computed(() => Math.max(1, Math.ceil(filteredNodes.value.length / 50)))
const pagedNodes = computed(() => filteredNodes.value.slice((page.value - 1) * 50, page.value * 50))
watch([search, nodeState, nodeSource, () => props.section], () => { page.value = 1; selected.value = [] })
watch(() => props.section, () => { search.value = ''; nodeSource.value = ''; nodeState.value = '' })
watch(totalPages, count => { page.value = Math.min(page.value, count) })
function nodeSourceLabel(node: ManagedNode) {
  if (node.dynamic) return text('动态代理', 'Dynamic proxy')
  return node.subscription_ids?.map(id => status.value?.subscription_items?.find(s => s.id === id)?.label || id.slice(0, 8)).join(', ') || text('订阅（待更新来源）', 'Subscription (refresh to identify)')
}
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
function scheduleRefresh() {
  if (timer) clearTimeout(timer)
  if (!disposed && !pending.value && (status.value?.busy || warmPools.value.some(p => p.target > 0))) timer = setTimeout(refresh, status.value?.busy ? 1500 : 5000)
}
async function refresh() {
  if (timer) clearTimeout(timer)
  try {
    const { data } = await apiClient.get<Status>('/admin/system/mihomo')
    if (!disposed) { status.value = data; error.value = '' }
  } catch { if (!disposed) error.value = text('无法读取内核状态', 'Cannot read kernel status') }
  finally { loading.value = false; scheduleRefresh() }
}
async function operate(action: string, payload: Payload = {}): Promise<boolean> {
  pending.value = true; error.value = ''; success.value = ''
  if (timer) clearTimeout(timer)
  try {
    status.value = (await apiClient.post<Status>('/admin/system/mihomo', { action, subscriptions: [], dynamic_proxies: [], append: false, ...payload })).data
    while (status.value.busy && !disposed) {
      await new Promise(resolve => setTimeout(resolve, 1500))
      if (disposed) return false
      status.value = (await apiClient.get<Status>('/admin/system/mihomo')).data
    }
    if (disposed) return false
    if (status.value.error) { error.value = status.value.error; return false }
    success.value = text('变更已应用', 'Changes applied')
    return true
  } catch (cause: unknown) {
    error.value = (cause as { message?: string })?.message || text('操作未完成，请重试', 'Operation failed. Please retry.')
    return false
  } finally { pending.value = false; scheduleRefresh() }
}
async function saveDownloadMode() {
  pending.value = true; error.value = ''; success.value = ''
  if (timer) clearTimeout(timer)
  try {
    status.value = (await apiClient.put<Status>('/admin/system/mihomo/download-mode', { mode: downloadMode.value })).data
    downloadModeDirty.value = false
    downloadMode.value = status.value.subscription_download_mode || 'auto'
    success.value = text('下载模式已保存', 'Download mode saved')
  } catch (cause: unknown) {
    error.value = (cause as { message?: string })?.message || text('无法保存下载模式', 'Cannot save download mode')
  } finally { pending.value = false; scheduleRefresh() }
}
function openSubscription(source?: Subscription) {
  editing.value = source; subscriptionName.value = source?.label || ''; subscriptions.value = ''; error.value = ''; subscriptionDialog.value = true
}
async function saveSubscription() {
  const urls = subscriptions.value.split(/\r?\n/).map(s => s.trim()).filter(Boolean)
  const action = editing.value ? (urls.length ? 'subscription_update/' : 'subscription_rename/') + editing.value.id : 'subscription_add'
  if (await operate(action, { subscriptions: urls, name: subscriptionName.value.trim() })) { subscriptions.value = ''; subscriptionDialog.value = false }
}
function dynamicPayload(): Payload {
  return { dynamic_proxies: dynamicProxies.value.split(/\r?\n/).map(s => s.trim()).filter(Boolean).map(s => s.includes('://') ? s : dynamicProtocol.value + '://' + s) }
}
async function saveDynamic() {
  if (replaceDynamic.value && status.value?.dynamic_proxies) {
    confirmation.value = { actions: ['dynamic_replace'], payload: dynamicPayload(), message: text('将替换全部已保存动态代理，可能中断使用旧出口的会话。机场订阅会保留。', 'This replaces all saved dynamic proxies and may interrupt sessions using old exits. Subscriptions are preserved.') }
  } else if (await operate(replaceDynamic.value ? 'dynamic_replace' : 'dynamic_append', dynamicPayload())) { dynamicProxies.value = ''; dynamicDialog.value = false }
}
function confirmRemoval(actions: string[]) {
  confirmation.value = { actions, message: text('移除这些来源及其独有节点？其他来源会保留，正在使用被移除出口的会话可能受影响。移除最后一个来源后将拒绝代理流量。', 'Remove these sources and their exclusive nodes? Other sources are preserved; sessions using removed exits may be affected. Removing the last source rejects proxy traffic.') }
}
async function confirmActions() {
  const task = confirmation.value
  confirmation.value = undefined
  if (!task) return
  for (const action of task.actions) {
    if (!await operate(action, task.payload)) return
    selected.value = selected.value.filter(id => !action.endsWith('/' + id))
  }
  if (task.actions.includes('dynamic_replace')) { dynamicProxies.value = ''; dynamicDialog.value = false }
}
async function batchSubscriptions(action: string) {
  const targets = [...selected.value]
  for (const id of targets) {
    if (action === 'subscription_refresh' && !status.value?.subscription_items?.find(s => s.id === id)?.enabled) continue
    if (!await operate(action + '/' + id)) return
  }
  selected.value = []
}
async function importFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  if (file.size > 256 * 1024) { error.value = text('文件不能超过 256 KiB', 'File must be at most 256 KiB'); return }
  try { subscriptions.value = await file.text(); input.value = '' } catch { error.value = text('无法读取文件', 'Cannot read file') }
}
onMounted(refresh)
onUnmounted(() => { disposed = true; if (timer) clearTimeout(timer) })
</script>
