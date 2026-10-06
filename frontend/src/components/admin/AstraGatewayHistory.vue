<template>
  <section class="card space-y-3 p-5" aria-labelledby="astra-history-title">
    <h2 id="astra-history-title" class="font-semibold">{{ t(`${p}.historyTitle`) }}</h2>
    <p class="text-xs text-gray-500">{{ t(`${p}.historyHint`) }}</p>
    <form class="flex flex-wrap items-center gap-3" @submit.prevent="search">
      <input v-model="host" class="input w-72" :placeholder="t(`${p}.historySearch`)" :aria-label="t(`${p}.historySearch`)" maxlength="200" />
      <label class="flex items-center gap-2 text-sm"><input v-model="passed" type="checkbox" @change="search" />{{ t(`${p}.historyPassed`) }}</label>
      <button class="btn btn-secondary btn-sm" :disabled="loading">{{ t(`${p}.historyQuery`) }}</button>
    </form>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ t(`${p}.historyError`) }}</p>
    <p class="text-sm">{{ t(`${p}.historyCount`, { n: data.unique_gateways, records: data.total }) }}</p>
    <div class="overflow-x-auto">
      <table class="min-w-[640px] w-full text-left text-sm">
        <thead><tr class="border-b dark:border-dark-600"><th class="p-2">{{ t(`${p}.gateway`) }}</th><th class="p-2">{{ t(`${p}.sources`) }}</th><th class="p-2">{{ t(`${p}.targets`) }}</th><th class="p-2">{{ t(`${p}.historyResults`) }}</th><th class="p-2">{{ t(`${p}.historyLastPass`) }}</th><th class="p-2">{{ t(`${p}.historyLastFailure`) }}</th><th class="p-2">{{ t(`${p}.historyLastResult`) }}</th></tr></thead>
        <tbody><tr v-for="row in data.items" :key="`${row.gateway}/${row.source_account_id}/${row.target_account_id}`" class="border-b dark:border-dark-600"><td class="p-2 font-mono text-xs">{{ row.gateway }}</td><td class="p-2">#{{ row.source_account_id }}</td><td class="p-2">{{ row.target_account_id ? `#${row.target_account_id}` : t(`${p}.historySourceOnly`) }}</td><td class="p-2">{{ row.passes }} / {{ row.failures }}</td><td class="p-2">{{ clock(row.last_pass) }}</td><td class="p-2">{{ clock(row.last_failure) }}</td><td class="p-2">{{ reason(row.last_reason) }}<span v-if="row.last_answer"> · {{ row.last_answer }}</span></td></tr></tbody>
      </table>
    </div>
    <p v-if="!loading && !data.items.length && !error" class="text-sm text-gray-500">{{ t(`${p}.historyEmpty`) }}</p>
    <div class="flex items-center gap-3 text-sm"><button class="btn btn-secondary btn-sm" :disabled="loading || page <= 1" @click="changePage(-1)">{{ t(`${p}.previousPage`) }}</button><span>{{ page }}</span><button class="btn btn-secondary btn-sm" :disabled="loading || page * 20 >= data.total" @click="changePage(1)">{{ t(`${p}.nextPage`) }}</button></div>
  </section>
</template>
<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getAstraGatewayHistory, type AstraGatewayHistoryPage } from '@/api/admin/astraGateway'
const { t, te } = useI18n()
const p = 'admin.astraGateway'
const host = ref(''), passed = ref(true), page = ref(1), loading = ref(false), error = ref(false)
const data = ref<AstraGatewayHistoryPage>({ items: [], total: 0, unique_gateways: 0 })
let request = 0, alive = true, timer: ReturnType<typeof setInterval> | undefined
function clock(value?: string) { return value ? new Date(value).toLocaleString() : '—' }
function reason(code: string) { const key = `${p}.reasons.${code}`; return te(key) ? t(key) : code }
async function refresh() {
  const current = ++request
  loading.value = true
  try { const result = await getAstraGatewayHistory(host.value.trim(), passed.value, page.value); if (alive && current === request) { data.value = result; error.value = false } }
  catch { if (alive && current === request) error.value = true }
  finally { if (alive && current === request) loading.value = false }
}
function search() { page.value = 1; void refresh() }
function changePage(delta: number) { page.value += delta; void refresh() }
onMounted(() => { void refresh(); timer = setInterval(() => { if (!loading.value) void refresh() }, 15000) })
onUnmounted(() => { alive = false; clearInterval(timer) })
</script>
