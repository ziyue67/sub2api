<template>
  <AppLayout>
    <div class="min-w-0 space-y-5">
      <header><h1 class="text-2xl font-semibold">{{ t('priorityScheduling.title') }}</h1><p class="mt-2 text-sm text-gray-500">{{ t('priorityScheduling.description') }}</p></header>
      <SmartOpsNav />
      <p class="rounded-xl bg-primary-50 p-4 text-sm leading-6 text-primary-800 dark:bg-primary-950/30 dark:text-primary-200">{{ t('priorityScheduling.scopeNote') }}</p>
      <div v-if="error" role="alert" class="text-red-600">{{ error }} <button v-if="!draft" class="underline" @click="load">{{ t('priorityScheduling.retry') }}</button></div>
      <p v-if="notice" role="status" class="text-emerald-600">{{ notice }}</p>
      <p v-if="loading">{{ t('priorityScheduling.loading') }}</p>
      <form v-if="draft" class="card p-5" @submit.prevent="save">
        <fieldset :disabled="saving" class="min-w-0 space-y-5">
          <label class="flex items-center gap-3 font-medium"><input v-model="draft.enabled" data-testid="enabled" type="checkbox" role="switch" />{{ t('priorityScheduling.enabled') }}</label>
          <div class="grid gap-5 lg:grid-cols-2">
            <section class="min-w-0 space-y-4">
              <label class="block"><span class="field-title">{{ t('priorityScheduling.strategy') }}</span><select v-model="draft.mode" class="input w-full" data-testid="mode"><option v-for="mode in modes" :key="mode" :value="mode">{{ t(`priorityScheduling.modes.${mode}`) }}</option></select></label>
              <div class="grid grid-cols-2 gap-3"><label v-for="(weight, index) in weightKeys" :key="weight"><span class="field-title">{{ t(`priorityScheduling.${weight}`) }}</span><input v-if="draft.mode === 'custom'" v-model.number="draft[weight]" type="number" min="0" max="100" step="0.1" required class="input w-full" /><div v-else class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800">{{ presets[draft.mode][index] }}%</div></label></div>
              <p class="text-sm leading-6 text-gray-500">{{ t('priorityScheduling.rule') }}</p>
              <p class="text-xs leading-5 text-gray-500">{{ t('priorityScheduling.costHint') }}</p>
            </section>
            <section><h2 class="mb-3 font-medium">{{ t('priorityScheduling.thresholds') }}</h2><div class="grid gap-3 sm:grid-cols-2"><label v-for="field in fields" :key="field.key"><span class="field-title">{{ t(`priorityScheduling.${field.key}`) }}</span><input v-model.number="draft[field.key]" type="number" :min="field.min" :max="field.max" step="1" required class="input w-full" /></label></div></section>
          </div>
          <div class="grid gap-5 sm:grid-cols-2"><label><span class="field-title">{{ t('priorityScheduling.groupIds') }}</span><input v-model="groups" class="input w-full" data-testid="groups" /><small class="text-gray-500">{{ t('priorityScheduling.groupHint') }}</small></label><label><span class="field-title">{{ t('priorityScheduling.models') }}</span><textarea v-model="modelsText" class="input w-full" rows="3" /><small class="text-gray-500">{{ t('priorityScheduling.modelHint') }}</small></label></div>
          <button class="btn btn-primary" type="submit">{{ t(saving ? 'priorityScheduling.saving' : 'priorityScheduling.save') }}</button>
        </fieldset>
      </form>
      <PriorityAccountBatch />
      <section class="card p-5">
        <header class="flex flex-wrap items-center justify-between gap-3"><h2 class="text-lg font-semibold">{{ t('priorityScheduling.recent') }}</h2><button class="btn btn-secondary" :disabled="refreshing" @click="refresh">{{ t('priorityScheduling.refresh') }}</button></header>
        <p class="my-3 text-xs leading-5 text-gray-500">{{ t('priorityScheduling.snapshotHint') }}</p>
        <p v-if="snapshotError" role="alert" class="text-red-600">{{ snapshotError }}</p>
        <p v-if="!snapshot" class="py-8 text-center text-sm text-gray-500">{{ t('priorityScheduling.empty') }}</p>
        <template v-else>
          <p class="mb-4 text-sm text-gray-500">{{ new Date(snapshot.at).toLocaleString() }} · {{ t('priorityScheduling.model') }}: {{ snapshot.model }} · {{ t('priorityScheduling.group') }}: {{ snapshot.group_id ?? '—' }} · {{ t(`priorityScheduling.modes.${snapshot.mode}`) }}</p>
          <p v-if="!snapshot.history_ready" class="text-sm text-amber-600">{{ t('priorityScheduling.historyPending') }}</p>
          <div v-else class="overflow-x-auto"><table class="w-full whitespace-nowrap text-left text-sm"><thead><tr><th v-for="key in ['account','tier','priority','score','quality','latency','load','rate','profit']" :key="key" class="px-3 py-3 text-xs font-medium text-gray-500">{{ t(`priorityScheduling.${key}`) }}</th></tr></thead><tbody><tr v-for="row in snapshot.candidates" :key="row.account_id" class="border-t border-gray-100 dark:border-dark-700"><td class="px-3 py-4"><strong class="font-medium">{{ row.account_name }}</strong><small class="block text-gray-400">#{{ row.account_id }}</small></td><td class="px-3 py-4"><span :class="row.tier === 'eligible' ? 'text-emerald-600' : row.tier === 'degraded' ? 'text-amber-600' : 'text-gray-500'">{{ t(`priorityScheduling.tiers.${row.tier}`) }}</span><small v-for="reason in row.reasons" :key="reason" class="block text-gray-400">{{ t(`priorityScheduling.reasons.${reason}`) }}</small></td><td class="px-3 py-4 tabular-nums">{{ row.priority }}</td><td class="px-3 py-4 tabular-nums">{{ row.score.toFixed(1) }}</td><td class="px-3 py-4">{{ row.quality_samples ? `${row.quality_passed}/${row.quality_samples}` : '—' }}</td><td class="px-3 py-4">{{ row.samples ? `${Math.round(row.p90_ttft_ms)} ms` : '—' }}<small class="block text-gray-400">{{ row.samples }} {{ t('priorityScheduling.samples') }}</small></td><td class="px-3 py-4">{{ row.load_percent == null ? '—' : `${row.load_percent}%` }}</td><td class="px-3 py-4">{{ row.rate == null ? '—' : `${row.rate.toFixed(3)}×` }}</td><td class="px-3 py-4"><template v-if="row.economics_source === 'usage'"><span :class="(row.profit ?? 0) < 0 ? 'text-red-600' : 'text-emerald-600'">${{ row.profit?.toFixed(4) }} · {{ row.margin == null ? '—' : `${(row.margin * 100).toFixed(1)}%` }}</span><small class="block text-gray-400">${{ row.revenue.toFixed(4) }} − ${{ row.theoretical_cost.toFixed(4) }}</small></template><span v-else class="text-xs text-gray-500">{{ t(`priorityScheduling.economics.${row.economics_source || 'unknown'}`) }}</span></td></tr></tbody></table></div>
        </template>
      </section>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import AppLayout from '@/components/layout/AppLayout.vue'
import PriorityAccountBatch from '@/components/admin/operations/PriorityAccountBatch.vue'
import SmartOpsNav from '@/components/admin/operations/SmartOpsNav.vue'
import { getPriorityConfig, savePriorityConfig, getPrioritySnapshot, type PrioritySchedulingConfig, type PrioritySnapshot } from '@/api/admin/priorityScheduling'
const { t } = useI18n(), auth = useAuthStore()
const draft = ref<PrioritySchedulingConfig | null>(null), snapshot = ref<PrioritySnapshot | null>(null)
const groups = ref(''), modelsText = ref(''), error = ref(''), snapshotError = ref(''), notice = ref('')
const loading = ref(false), saving = ref(false), refreshing = ref(false)
const modes = ['experience', 'balanced', 'profit', 'custom'] as const
const presets = { experience: [40,35,20,5], balanced: [30,25,25,20], profit: [20,15,20,45] }
const weightKeys = ['quality_weight','latency_weight','load_weight','cost_weight'] as const
const fields = [
  { key: 'target_ttft_ms', min: 100, max: 120000 }, { key: 'max_load_percent', min: 10, max: 100 },
  { key: 'min_quality_percent', min: 0, max: 100 }, { key: 'window_minutes', min: 5, max: 1440 },
  { key: 'min_samples', min: 1, max: 1000 }, { key: 'quality_max_age_hours', min: 1, max: 168 }
] as const
let alive = true, generation = 0
const active = (version: number) => alive && generation === version
async function load() {
  if (loading.value) return
  const version = generation; loading.value = true; error.value = ''
  try {
    const config = await getPriorityConfig()
    if (!active(version)) return
    const { teams: _legacyTeams, ...currentConfig } = config as PrioritySchedulingConfig & { teams?: unknown }
    draft.value = currentConfig; groups.value = (config.group_ids ?? []).join(', '); modelsText.value = (config.models ?? []).join('\n')
  } catch { if (active(version)) error.value = t('priorityScheduling.error') }
  finally { if (active(version)) loading.value = false }
}
async function save() {
  if (!draft.value || saving.value) return
  const ids = groups.value.trim() ? groups.value.split(/[,，]/).map(v => Number(v.trim())) : []
  if (ids.some(id => !Number.isSafeInteger(id) || id <= 0)) { error.value = t('priorityScheduling.invalid'); return }
  const version = generation; saving.value = true; error.value = ''; notice.value = ''
  try {
    const config = await savePriorityConfig({ ...draft.value, group_ids: [...new Set(ids)], models: [...new Set(modelsText.value.split('\n').map(v => v.trim()).filter(Boolean))] })
    if (active(version)) { draft.value = config; notice.value = t('priorityScheduling.saved') }
  } catch { if (active(version)) error.value = t('priorityScheduling.error') }
  finally { if (active(version)) saving.value = false }
}
async function refresh() {
  if (refreshing.value) return
  const version = generation; refreshing.value = true; snapshotError.value = ''
  try { const value = await getPrioritySnapshot(); if (active(version)) snapshot.value = value }
  catch { if (active(version)) snapshotError.value = t('priorityScheduling.error') }
  finally { if (active(version)) refreshing.value = false }
}
watch(() => auth.user ? `${auth.user.id}:${auth.user.role}` : '', () => { generation++; draft.value = null; snapshot.value = null; groups.value = modelsText.value = error.value = notice.value = snapshotError.value = ''; loading.value = saving.value = refreshing.value = false }, { flush: 'sync' })
onMounted(() => { void load(); void refresh() })
onBeforeUnmount(() => { alive = false; generation++ })
</script>
<style scoped>
.card { @apply rounded-2xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900; }
.field-title { @apply mb-2 block text-xs font-medium text-gray-600 dark:text-gray-300; }
input[type=checkbox] { @apply h-4 w-4 rounded text-primary-600; }
button:disabled { @apply cursor-not-allowed opacity-50; }
</style>
