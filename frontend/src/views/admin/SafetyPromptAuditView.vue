<template>
  <AppLayout>
    <div class="w-full min-w-0 pb-8" data-testid="safety-prompt-audit">
      <SecurityAuditNav active="/admin/safety-prompt-audit" />
      <header class="mb-6 flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.16em] text-primary-600 dark:text-primary-400">{{ t('nav.securityAudit') }}</p>
          <h1 class="mt-1 text-2xl font-semibold tracking-tight text-gray-950 dark:text-white">{{ t('admin.safetyPromptAudit.title') }}</h1>
          <p class="mt-2 max-w-3xl text-sm text-gray-500 dark:text-dark-300">{{ t('admin.safetyPromptAudit.description') }}</p>
        </div>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="load">{{ t('admin.safetyPromptAudit.refresh') }}</button>
      </header>

      <p v-if="loading" role="status" class="card px-5 py-8 text-sm text-gray-500">{{ t('admin.safetyPromptAudit.loading') }}</p>
      <div v-else-if="error" role="alert" class="card px-5 py-8">
        <p class="text-sm text-red-600 dark:text-red-300">{{ t('admin.safetyPromptAudit.loadFailed') }}</p>
        <button type="button" class="btn btn-secondary btn-sm mt-3" @click="load">{{ t('admin.safetyPromptAudit.refresh') }}</button>
      </div>
      <template v-else>
        <section class="card px-5 py-4">
          <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.safetyPromptAudit.module') }}</h2>
          <div class="mt-4 grid gap-4 lg:grid-cols-2">
            <label class="flex items-start gap-3 text-sm">
              <input v-model="form.enabled" type="checkbox" class="mt-1" data-testid="safety-prompt-enabled" />
              <span><strong class="block">{{ t('admin.safetyPromptAudit.enabled') }}</strong><span class="text-xs text-gray-500">{{ t('admin.safetyPromptAudit.enabledHint') }}</span></span>
            </label>
            <label class="flex items-start gap-3 text-sm">
              <input v-model="form.signal_enabled" type="checkbox" class="mt-1" data-testid="safety-prompt-signal" />
              <span><strong class="block">{{ t('admin.safetyPromptAudit.signalEnabled') }}</strong><span class="text-xs text-gray-500">{{ t('admin.safetyPromptAudit.signalHint') }}</span></span>
            </label>
            <label class="text-sm">{{ t('admin.safetyPromptAudit.retentionDays') }}
              <input v-model.number="form.retention_days" type="number" min="1" max="90" class="input mt-1 w-full" data-testid="safety-prompt-retention" />
              <span class="mt-1 block text-xs text-gray-500">{{ t('admin.safetyPromptAudit.retentionHint') }}</span>
            </label>
            <div class="flex items-end">
              <button type="button" class="btn btn-primary btn-sm" :disabled="saving" @click="save">{{ saving ? t('admin.safetyPromptAudit.saving') : t('admin.safetyPromptAudit.save') }}</button>
            </div>
          </div>
          <p v-if="notice" role="status" class="mt-3 text-sm text-emerald-600 dark:text-emerald-300">{{ notice }}</p>
          <p v-if="saveError" role="alert" class="mt-3 text-sm text-red-600 dark:text-red-300">{{ saveError }}</p>
          <p class="mt-4 text-xs text-gray-500">{{ t('admin.safetyPromptAudit.scopeNote') }}</p>
          <p v-if="stats" class="mt-2 text-xs" :class="stats.persistent && !stats.warning ? 'text-gray-500' : 'text-amber-700 dark:text-amber-300'" data-testid="safety-prompt-storage">
            {{ stats.persistent && !stats.warning ? t('admin.safetyPromptAudit.storageReady', { count: stats.count, limit: stats.limit }) : t('admin.safetyPromptAudit.storageWarning', { warning: stats.warning || '—' }) }}
          </p>
          <p v-if="!form.enabled" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.safetyPromptAudit.disabledNotice') }}</p>
        </section>

        <section class="card mt-4 px-4 py-4 sm:px-6">
          <div class="grid gap-3 sm:grid-cols-3">
            <label class="text-xs text-gray-500">{{ t('admin.safetyPromptAudit.filters.from') }}<input v-model="from" type="date" class="input mt-1 w-full" /></label>
            <label class="text-xs text-gray-500">{{ t('admin.safetyPromptAudit.filters.to') }}<input v-model="to" type="date" class="input mt-1 w-full" /></label>
            <label class="text-xs text-gray-500">{{ t('admin.safetyPromptAudit.filters.search') }}<input v-model="search" type="search" class="input mt-1 w-full" /></label>
          </div>
          <div class="mt-3 flex flex-wrap items-center gap-2">
            <button type="button" class="btn btn-secondary btn-sm" @click="setToday">{{ t('admin.safetyPromptAudit.filters.today') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" @click="clearFilters">{{ t('admin.safetyPromptAudit.filters.all') }}</button>
            <span class="text-xs text-gray-500" data-testid="safety-prompt-count">{{ countText }}</span>
          </div>
          <div v-if="badRange" role="alert" class="mt-3 text-sm text-amber-700 dark:text-amber-300">{{ t('admin.safetyPromptAudit.badRange') }}</div>
          <div v-else-if="filtered.length" class="mt-4 overflow-x-auto" data-testid="safety-prompt-table">
            <table class="w-full whitespace-nowrap text-left text-sm">
              <thead><tr>
                <th class="px-3 py-2 text-xs font-medium text-gray-500">{{ t('admin.safetyPromptAudit.table.time') }}</th>
                <th class="px-3 py-2 text-xs font-medium text-gray-500">{{ t('admin.safetyPromptAudit.table.model') }}</th>
                <th class="px-3 py-2 text-xs font-medium text-gray-500">{{ t('admin.safetyPromptAudit.table.user') }}</th>
                <th class="px-3 py-2 text-xs font-medium text-gray-500">{{ t('admin.safetyPromptAudit.table.summary') }}</th>
              </tr></thead>
              <tbody>
                <tr v-for="event in filtered" :key="event.id" class="border-t border-gray-100 align-top dark:border-dark-700">
                  <td class="px-3 py-3"><span class="block">{{ formatTime(event.time) }}</span><span class="block text-xs text-gray-400">{{ event.id }}</span><span v-if="event.request_id" class="block text-xs text-gray-400">{{ t('admin.safetyPromptAudit.requestId') }}: {{ event.request_id }}</span></td>
                  <td class="px-3 py-3"><span class="block">{{ event.model || '—' }}</span><span class="block text-xs text-gray-400">{{ t('admin.safetyPromptAudit.upstreamAccount') }}: {{ event.upstream_account_id ?? '—' }}</span></td>
                  <td class="px-3 py-3 text-xs text-gray-500">{{ userLabel(event) }}</td>
                  <td class="max-w-xl whitespace-pre-wrap break-words px-3 py-3 text-xs">{{ event.summary?.trim() || t('admin.safetyPromptAudit.noSummary') }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-else class="mt-4 text-sm text-gray-500" data-testid="safety-prompt-empty">{{ t('admin.safetyPromptAudit.empty') }}</p>
          <div class="mt-4 flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!filtered.length" @click="exportJson">{{ t('admin.safetyPromptAudit.exportJson') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!filtered.length" @click="exportCsv">{{ t('admin.safetyPromptAudit.exportCsv') }}</button>
          </div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import SecurityAuditNav from '@/components/admin/security/SecurityAuditNav.vue'
import { getSafetyPromptConfig, listSafetyPromptEvents, updateSafetyPromptConfig, type SafetyPromptConfig, type SafetyPromptEvent, type SafetyPromptStats } from '@/api/admin/safetyPrompt'

const { t } = useI18n()
const stats = ref<SafetyPromptStats | null>(null)
const events = ref<SafetyPromptEvent[]>([])
const form = reactive<SafetyPromptConfig>({ enabled: false, signal_enabled: false, retention_days: 30 })
const loading = ref(true)
const saving = ref(false)
const error = ref('')
const saveError = ref('')
const notice = ref('')
const from = ref('')
const to = ref('')
const search = ref('')

const badRange = computed(() => Boolean(from.value && to.value && from.value > to.value))
const filtered = computed(() => {
  if (badRange.value) return []
  const query = search.value.trim().toLowerCase()
  return events.value.filter(event => {
    const date = dayOf(event.time)
    if (from.value && date < from.value) return false
    if (to.value && date > to.value) return false
    if (!query) return true
    return [event.id, event.request_id, event.model, event.upstream_account_id, event.summary]
      .filter(value => value != null)
      .join(' ')
      .toLowerCase()
      .includes(query)
  })
})
const countText = computed(() => badRange.value ? t('admin.safetyPromptAudit.badRange') : t('admin.safetyPromptAudit.count', { shown: filtered.value.length, total: events.value.length }))

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const [configResult, eventsResult] = await Promise.all([getSafetyPromptConfig(), listSafetyPromptEvents({})])
    stats.value = configResult.stats
    Object.assign(form, configResult.config)
    events.value = eventsResult.events || []
    if (eventsResult.stats) stats.value = eventsResult.stats
  } catch {
    error.value = t('admin.safetyPromptAudit.loadFailed')
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  saving.value = true
  notice.value = ''
  saveError.value = ''
  try {
    const result = await updateSafetyPromptConfig({ enabled: form.enabled, signal_enabled: form.signal_enabled, retention_days: Number(form.retention_days) })
    Object.assign(form, result.config)
    notice.value = t('admin.safetyPromptAudit.saved')
    await load()
  } catch {
    saveError.value = t('admin.safetyPromptAudit.saveFailed')
  } finally {
    saving.value = false
  }
}

function dayOf(value: string): string {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return ''
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}
function formatTime(value: string): string {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : '—'
}
function userLabel(event: SafetyPromptEvent): string {
  if (event.user_id != null) return `#${event.user_id}`
  if (event.identity && event.identity !== 'unknown') return event.identity
  return t('admin.safetyPromptAudit.unknownUser')
}
function setToday(): void {
  from.value = to.value = dayOf(new Date().toISOString())
  search.value = ''
}
function clearFilters(): void {
  from.value = to.value = search.value = ''
}
function download(filename: string, content: string, type: string): void {
  const url = URL.createObjectURL(new Blob([content], { type }))
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}
function exportRows() {
  return filtered.value.map(event => ({
    time: event.time,
    id: event.id,
    request_id: event.request_id ?? '',
    upstream_account_id: event.upstream_account_id ?? '',
    user_id: event.user_id ?? null,
    identity: event.identity ?? 'unknown',
    model: event.model ?? '',
    action: event.action,
    summary: event.summary ?? '',
    request_sha256: event.request_sha256 ?? ''
  }))
}
function exportJson(): void {
  if (!filtered.value.length) return
  download(`safety-injection-audit-${dayOf(new Date().toISOString())}.json`, JSON.stringify({ exported_at: new Date().toISOString(), records: exportRows() }, null, 2), 'application/json')
}
function csvCell(value: unknown): string {
  let text = value == null ? '' : String(value)
  if (/^\s*[=+@-]/.test(text)) text = `'${text}`
  return `"${text.replace(/"/g, '""')}"`
}
function exportCsv(): void {
  if (!filtered.value.length) return
  const rows = exportRows()
  const columns = Object.keys(rows[0]) as (keyof ReturnType<typeof exportRows>[number])[]
  const content = columns.map(csvCell).join(',') + '\r\n' + rows.map(row => columns.map(column => csvCell(row[column])).join(',')).join('\r\n')
  download(`safety-injection-audit-${dayOf(new Date().toISOString())}.csv`, content, 'text/csv;charset=utf-8')
}

onMounted(load)
</script>
