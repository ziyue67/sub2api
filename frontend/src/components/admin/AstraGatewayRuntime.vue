<template>
  <section class="card space-y-4 p-5" aria-labelledby="astra-runtime-title">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div><h2 id="astra-runtime-title" class="font-semibold">{{ t(`${p}.runtimeTitle`) }}</h2><p class="mt-1 text-xs text-gray-500">{{ t(`${p}.runtimeHint`) }}</p></div>
      <button type="button" class="btn btn-primary btn-sm" data-testid="prepare" :disabled="busy || setupBusy || dirty || !settings?.cookie_pool.enabled" @click="run('prepare')">{{ t(busy ? `${p}.testing` : `${p}.prepare`) }}</button>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="dirty" class="text-sm text-amber-600">{{ t(`${p}.saveFirst`) }}</p>
    <p class="text-xs text-gray-500" data-testid="probe-hint">{{ t(`${p}.probeHint`) }}</p>
    <template v-if="runtime">
      <div v-if="runtime.setup?.state" class="rounded-xl border p-3 text-sm dark:border-dark-600" data-testid="setup-status">
        {{ t(`${p}.automaticStatus`) }}：{{ t(`${p}.setupStates.${runtime.setup.state}`) }}
        <span v-if="runtime.setup.phase"> · {{ t(`${p}.setupPhases.${runtime.setup.phase}`) }}</span>
        <span v-if="runtime.setup.account_id"> #{{ runtime.setup.account_id }}</span>
        <span v-if="runtime.setup.reason"> · {{ reason(runtime.setup.reason) }}</span>
      </div>
      <p class="text-sm" :class="runtime.ready_routes ? 'text-emerald-600' : 'text-amber-600'">{{ t(`${p}.readyRoutes`, { n: runtime.ready_routes }) }} · {{ t(runtime.preparing ? `${p}.preparing` : `${p}.idle`) }}</p>
      <div v-if="runtime.cooldowns?.length" class="rounded-xl border p-3 text-sm dark:border-dark-600" data-testid="rotation-cooldowns">
        <h3 class="font-medium">{{ t(`${p}.cooldownTitle`) }}</h3>
        <p v-for="row in runtime.cooldowns" :key="`${row.account_id}:${row.gateway}`" class="mt-2 break-all">#{{ row.account_id }} · {{ row.gateway }} · {{ remaining(row.retry_at) }} s</p>
      </div>
      <div class="space-y-2 rounded-xl border p-3 dark:border-dark-600" data-testid="gateway-observations">
        <h3 class="text-sm font-medium">{{ t(`${p}.gatewayInventory`) }}</h3>
        <p class="text-sm">{{ t(`${p}.gatewayCounts`, { n: runtime.gateways?.length || 0, repeats: repeatedGatewayHits, unknown: runtime.unknown_gateway_samples || 0 }) }}</p>
        <p v-if="runtime.gateways?.length === 1" class="text-xs text-amber-600">{{ t(`${p}.singleGateway`) }}</p>
        <p v-if="!runtime.gateways?.length" class="text-xs text-gray-500">{{ t(`${p}.noKnownGateway`) }}</p>
        <p class="text-xs text-gray-500">{{ t(`${p}.gatewayObservationHint`) }}</p>
        <div v-if="runtime.gateways?.length" class="overflow-x-auto">
          <table class="min-w-[640px] w-full text-left text-sm">
            <thead><tr class="border-b dark:border-dark-600"><th class="p-2">{{ t(`${p}.gateway`) }}</th><th class="p-2">{{ t(`${p}.sources`) }}</th><th class="p-2">{{ t(`${p}.samples`) }}</th><th class="p-2">{{ t(`${p}.sourceChecks`) }}</th><th class="p-2">{{ t(`${p}.targetChecks`) }}</th><th class="p-2">{{ t(`${p}.lastObserved`) }}</th></tr></thead>
            <tbody><tr v-for="node in runtime.gateways" :key="node.gateway" class="border-b dark:border-dark-600"><td class="p-2 font-mono text-xs">{{ node.gateway }}</td><td class="p-2">{{ node.source_account_ids.map(id => `#${id}`).join(', ') }}</td><td class="p-2">{{ node.samples }}</td><td class="p-2">{{ node.source_passes }} / {{ node.source_failures }}</td><td class="p-2">{{ node.target_passes }} / {{ node.target_failures }}</td><td class="p-2">{{ clock(node.last_seen) }}</td></tr></tbody>
          </table>
        </div>
      </div>
      <div class="overflow-x-auto">
        <table class="min-w-[640px] w-full text-left text-sm">
          <thead><tr class="border-b dark:border-dark-600"><th class="p-2">{{ t(`${p}.account`) }}</th><th class="p-2">{{ t(`${p}.state`) }}</th><th class="p-2">{{ t(`${p}.gateway`) }}</th><th class="p-2">{{ t(`${p}.remaining`) }}</th><th class="p-2">{{ t(`${p}.checked`) }}</th></tr></thead>
          <tbody><tr v-for="row in runtime.sources" :key="row.account_id" class="border-b dark:border-dark-600"><td class="p-2">#{{ row.account_id }}</td><td class="p-2">{{ reason(row.reason) }}</td><td class="p-2 font-mono text-xs">{{ row.gateway || '—' }}<span v-if="row.proxy_node" class="mt-1 block font-sans">{{ row.proxy_country || '—' }} · {{ row.proxy_node }}</span></td><td class="p-2">{{ routeLifetime(row) }}</td><td class="p-2">{{ clock(row.checked_at) }}</td></tr></tbody>
        </table>
      </div>
      <div class="grid gap-3 md:grid-cols-2">
        <div v-for="row in runtime.targets" :key="`target-${row.account_id}`" class="rounded-xl border p-3 dark:border-dark-600">
          <p class="text-sm font-medium">{{ t(`${p}.targets`) }} #{{ row.account_id }}</p><p class="my-2 text-xs text-gray-500">{{ reason(row.reason) }} · {{ routeLifetime(row) }}</p>
          <p class="mb-2 break-all font-mono text-xs">{{ row.gateway || t(`${p}.unknownGateway`) }}</p>
          <p v-if="row.proxy_node" class="mb-2 text-xs">{{ row.proxy_country || '—' }} · {{ row.proxy_node }}</p>
          <p v-if="row.answer" class="mb-2 text-xs">{{ t(`${p}.actual`) }}: {{ row.answer }}</p>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || setupBusy || dirty || !settings?.cookie_pool.enabled" @click="run('verify', row.account_id)">{{ t(`${p}.verifyTarget`) }}</button>
        </div>
        <div v-for="row in runtime.ws" :key="`ws-${row.account_id}`" class="rounded-xl border p-3 dark:border-dark-600">
          <p class="text-sm font-medium">WS #{{ row.account_id }}</p><p class="my-2 text-xs text-gray-500">{{ reason(row.reason) }} · {{ t(`${p}.sessions`, { n: row.active_sessions }) }} · {{ remaining(row.expires_at) }} s</p>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || setupBusy || dirty || !row.ready" @click="run('ws', row.account_id)">{{ t(`${p}.verifyWS`) }}</button>
        </div>
      </div>
      <div v-if="runtime.last_test" role="status" class="space-y-3 rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-700">
        <p>{{ clock(runtime.last_test.checked_at) }} · {{ runtime.last_test.action }} <span v-if="runtime.last_test.account_id">#{{ runtime.last_test.account_id }}</span> · {{ reason(runtime.last_test.reason) }} · {{ (runtime.last_test.duration_ms / 1000).toFixed(1) }} s</p>
      </div>
    </template>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getAstraGatewayRuntime, testAstraGateway, type AstraGatewayRuntime, type AstraGatewaySettings } from '@/api/admin/astraGateway'
defineProps<{ settings?: AstraGatewaySettings; dirty: boolean }>()
const { t, te } = useI18n()
const p = 'admin.astraGateway'
const emit = defineEmits<{ scheduling: [records: NonNullable<AstraGatewayRuntime['scheduling_records']>] }>()
const runtime = ref<AstraGatewayRuntime>()
const repeatedGatewayHits = computed(() => runtime.value?.gateways?.reduce((sum, node) => sum + node.repeated_hits, 0) || 0)
const error = ref('')
const busy = ref(false)
const setupBusy = computed(() => ['queued', 'running'].includes(runtime.value?.setup?.state || ''))
const now = ref(Date.now())
let poll: ReturnType<typeof setInterval> | undefined
let tick: ReturnType<typeof setInterval> | undefined
let alive = true
let refreshing = false
function reason(code: string) { const key = `${p}.reasons.${code}`; return te(key) ? t(key) : code }
function routeLifetime(row: { state?: string; expires_at?: string }) { return row.state === 'ready' && row.expires_at && remaining(row.expires_at) > 0 ? `${remaining(row.expires_at)} s` : '—' }
function clock(value?: string) { return value ? new Date(value).toLocaleString() : '—' }
function remaining(expiry?: string) { return expiry ? Math.max(0, Math.ceil((Date.parse(expiry) - now.value) / 1000)) : 0 }
async function refresh() {
  if (refreshing) return
  refreshing = true
  try { const result = await getAstraGatewayRuntime(); if (alive) { runtime.value = result; emit('scheduling', result.scheduling_records || []); error.value = '' } }
  catch { if (alive) error.value = t(`${p}.runtimeError`) }
  finally { refreshing = false }
}
async function run(action: 'prepare' | 'verify' | 'ws', account = 0) {
  busy.value = true; error.value = ''
  try { const result = await testAstraGateway(action, account, 'state_probe'); await refresh(); if (runtime.value) runtime.value.last_test = result }
  catch { error.value = t(`${p}.testError`) }
  finally { busy.value = false }
}
onMounted(() => { void refresh(); poll = setInterval(refresh, 5000); tick = setInterval(() => { now.value = Date.now() }, 1000) })
onUnmounted(() => { alive = false; clearInterval(poll); clearInterval(tick) })
</script>
