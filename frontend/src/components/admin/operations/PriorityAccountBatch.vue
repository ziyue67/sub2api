<template>
  <section class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-900">
    <h2 class="text-lg font-semibold">{{ t('priorityScheduling.batch.title') }}</h2>
    <p class="my-3 text-sm leading-6 text-gray-500">{{ t('priorityScheduling.batch.hint') }}</p>
    <fieldset :disabled="loading || applying" class="min-w-0 space-y-4">
      <div class="flex flex-wrap items-end gap-3"><label class="text-xs"><span class="mb-2 block">{{ t('priorityScheduling.batch.group') }}</span><input v-model="group" class="input w-40" type="number" min="1" @input="clearPreview" /></label><button class="btn btn-secondary" data-testid="load-accounts" @click="load">{{ t(loading ? 'priorityScheduling.loading' : 'priorityScheduling.batch.load') }}</button></div>
      <template v-if="rows.length">
        <div><h3 class="mb-2 text-sm font-medium">{{ t('priorityScheduling.batch.typeOrder') }}</h3><p class="mb-3 text-xs text-gray-500">{{ t('priorityScheduling.batch.typeOrderHint') }}</p>
          <ol class="grid gap-2 sm:grid-cols-2 lg:grid-cols-4" data-testid="type-order"><li v-for="(kind, index) in typeOrder" :key="kind" :data-type="kind" draggable="true" class="flex items-center gap-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600" @dragstart="dragged = kind" @dragend="dragged = null" @dragover.prevent @drop.prevent="drop(kind)"><span class="text-xs text-gray-400">{{ index + 1 }}</span><strong class="flex-1 text-sm">{{ typeLabels[kind] }}</strong><button type="button" class="rounded px-2 py-1 hover:bg-gray-100 dark:hover:bg-dark-700 disabled:opacity-30" :disabled="index === 0" :aria-label="t('priorityScheduling.batch.moveUp', { type: typeLabels[kind] })" @click="moveType(index, index - 1)">↑</button><button type="button" class="rounded px-2 py-1 hover:bg-gray-100 dark:hover:bg-dark-700 disabled:opacity-30" :disabled="index === typeOrder.length - 1" :aria-label="t('priorityScheduling.batch.moveDown', { type: typeLabels[kind] })" @click="moveType(index, index + 1)">↓</button></li></ol>
          <button type="button" class="mt-2 text-xs text-primary-600" @click="resetOrder">{{ t('priorityScheduling.batch.resetOrder') }}</button>
        </div>
        <p class="text-xs text-gray-500">{{ t('priorityScheduling.batch.priorityHint') }}</p>
        <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr><th class="p-2">{{ t('priorityScheduling.batch.category') }}</th><th class="p-2">{{ t('priorityScheduling.batch.accounts') }}</th><th class="p-2">{{ t('priorityScheduling.batch.current') }}</th><th class="p-2">{{ t('priorityScheduling.batch.priority') }}</th></tr></thead><tbody><tr v-for="row in rows" :key="row.key" class="border-t border-gray-100 dark:border-dark-700"><td class="whitespace-nowrap p-2"><label><input v-model="row.selected" type="checkbox" class="mr-2" />{{ label(row) }}</label></td><td class="p-2"><details><summary class="cursor-pointer">{{ row.accounts.length }}</summary><div class="max-h-36 overflow-auto text-xs text-gray-500"><div v-for="account in row.accounts" :key="account.id">#{{ account.id }} {{ account.name }}</div></div></details></td><td class="whitespace-nowrap p-2 text-xs text-gray-500">{{ range(row, 'priority') }} / {{ range(row, 'concurrency') }} / {{ range(row, 'load_factor') }}</td><td class="p-2"><input v-model.number="row.priority" :aria-label="`${label(row)} ${t('priorityScheduling.batch.priority')}`" type="number" min="0" max="1000000" step="1" class="input w-24" /></td></tr></tbody></table></div>
        <div class="grid gap-4 sm:grid-cols-2"><label><span class="mb-2 flex items-center gap-2 text-sm"><input v-model="setConcurrency" type="checkbox" />{{ t('priorityScheduling.batch.concurrency') }}</span><input v-model.number="concurrency" :disabled="!setConcurrency" type="number" min="1" max="10000" step="1" class="input w-full" data-testid="batch-concurrency" /></label><label><span class="mb-2 flex items-center gap-2 text-sm"><input v-model="setLoadFactor" type="checkbox" />{{ t('priorityScheduling.batch.loadFactor') }}</span><input v-model.number="loadFactor" :disabled="!setLoadFactor" type="number" min="1" max="10000" step="1" class="input w-full" data-testid="batch-load-factor" /></label></div>
        <p class="text-xs leading-5 text-gray-500">{{ t('priorityScheduling.batch.loadHint') }}</p>
        <p class="text-sm">{{ t('priorityScheduling.batch.preview', { count: selectedCount, concurrency: setConcurrency ? concurrency : t('priorityScheduling.batch.keep'), loadFactor: setLoadFactor ? loadFactor : t('priorityScheduling.batch.keep') }) }}</p>
        <button class="btn btn-primary" data-testid="apply-accounts" :disabled="!selectedCount" @click="apply">{{ t(applying ? 'priorityScheduling.saving' : 'priorityScheduling.batch.apply') }}</button>
      </template>
      <p v-else-if="loaded" class="text-sm text-gray-500">{{ t('priorityScheduling.batch.empty') }}</p>
    </fieldset>
    <p v-if="error" class="mt-3 text-sm text-red-600" role="alert">{{ error }}</p>
    <p v-if="notice" class="mt-3 text-sm text-emerald-600" role="status">{{ notice }}</p>
  </section>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { list, bulkUpdate } from '@/api/admin/accounts'
import { groupPriorityAccounts, priorityBatchPayloads, orderPriorityAccountRows, inferPriorityTypeOrder, defaultPriorityTypeOrder, type PriorityAccountKind, type PriorityAccountRow } from '@/utils/priorityAccountBatch'
import type { AccountListItem } from '@/types'
const { t } = useI18n(), auth = useAuthStore()
const typeOrder = ref<PriorityAccountKind[]>([...defaultPriorityTypeOrder]), dragged = ref<PriorityAccountKind | null>(null)
const typeLabels = { teams: 'Teams', pro: 'Pro', plus: 'Plus', api: 'API' }
const rows = ref<PriorityAccountRow[]>([]), group = ref(''), loading = ref(false), applying = ref(false), loaded = ref(false)
const setConcurrency = ref(false), setLoadFactor = ref(false), concurrency = ref(100), loadFactor = ref(10000)
const error = ref(''), notice = ref('')
const selectedCount = computed(() => rows.value.filter(r => r.selected).reduce((count, r) => count + r.accounts.length, 0))
let alive = true, version = 0
const active = (v: number) => alive && version === v
const label = (row: PriorityAccountRow) => row.kind === 'api' ? `API · ${row.rate}×` : row.kind === 'teams' ? 'OAuth · Teams' : row.kind === 'pro' ? 'OAuth · Pro' : row.kind === 'plus' ? 'OAuth · Plus' : t('priorityScheduling.batch.otherOAuth')
function range(row: PriorityAccountRow, key: 'priority' | 'concurrency' | 'load_factor') {
  const values = row.accounts.map(a => key === 'load_factor' ? a.load_factor ?? a.concurrency : a[key] as number)
  const min = Math.min(...values), max = Math.max(...values)
  return min === max ? String(min) : `${min}–${max}`
}
function clearPreview() { version++; rows.value = []; loaded.value = false; notice.value = error.value = '' }
function moveType(from: number, to: number) {
  if (loading.value || applying.value || from < 0 || to < 0 || from >= typeOrder.value.length || to >= typeOrder.value.length) return
  const order = [...typeOrder.value], [kind] = order.splice(from, 1)
  order.splice(to, 0, kind); typeOrder.value = order
  rows.value = orderPriorityAccountRows(rows.value, order)
}
function drop(kind: PriorityAccountKind) {
  if (dragged.value) moveType(typeOrder.value.indexOf(dragged.value), typeOrder.value.indexOf(kind))
  dragged.value = null
}
function resetOrder() {
  typeOrder.value = [...defaultPriorityTypeOrder]
  rows.value = orderPriorityAccountRows(rows.value, typeOrder.value)
}

async function load() {
  if (loading.value || applying.value) return
  if (group.value !== '' && (!Number.isSafeInteger(Number(group.value)) || Number(group.value) <= 0)) { error.value = t('priorityScheduling.invalid'); return }
  const v = ++version; loading.value = true; error.value = notice.value = ''; rows.value = []; loaded.value = false
  try {
    const accounts: AccountListItem[] = []
    for (let page = 1; page <= 100; page++) {
      const result = await list(page, 100, { platform: 'openai', group: group.value ? String(group.value) : undefined, lite: '1', include_scheduler_score: '0', sort_by: 'id', sort_order: 'asc' })
      if (!active(v)) return
      accounts.push(...result.items)
      if (page >= result.pages || accounts.length >= result.total) break
      if (page === 100) throw new Error('too many accounts')
    }
    const grouped = groupPriorityAccounts(accounts)
    typeOrder.value = inferPriorityTypeOrder(grouped)
    rows.value = orderPriorityAccountRows(grouped, typeOrder.value); loaded.value = true
  } catch { if (active(v)) error.value = t('priorityScheduling.batch.loadError') }
  finally { if (active(v)) loading.value = false }
}
async function apply() {
  if (applying.value || loading.value) return
  let batches: ReturnType<typeof priorityBatchPayloads>
  try { batches = priorityBatchPayloads(rows.value, setConcurrency.value ? concurrency.value : undefined, setLoadFactor.value ? loadFactor.value : undefined) }
  catch { error.value = t('priorityScheduling.invalid'); return }
  const v = version; applying.value = true; error.value = notice.value = ''; let succeeded = 0
  const done = new Set<number>()
  try {
    for (const batch of batches) {
      if (!active(v)) return
      const result = await bulkUpdate(batch)
      if (!active(v)) return
      for (const item of result.results) if (item.success) done.add(item.account_id)
      succeeded += result.success
    }
    if (active(v)) {
      rows.value = rows.value.map(row => ({ ...row, accounts: row.accounts.filter(a => !done.has(a.id)) })).filter(row => row.accounts.length)
      notice.value = t('priorityScheduling.batch.result', { count: succeeded })
      if (rows.value.some(row => row.selected && row.accounts.length)) error.value = t('priorityScheduling.batch.partial')
    }
  } catch {
    if (active(v)) { rows.value = rows.value.map(row => ({ ...row, accounts: row.accounts.filter(a => !done.has(a.id)) })).filter(row => row.accounts.length); notice.value = t('priorityScheduling.batch.result', { count: succeeded }); error.value = t('priorityScheduling.batch.partial') }
  } finally { if (active(v)) applying.value = false }
}
watch(() => auth.user ? `${auth.user.id}:${auth.user.role}` : '', () => { clearPreview(); loading.value = applying.value = false }, { flush: 'sync' })
onBeforeUnmount(() => { alive = false; version++ })
</script>
