<template>
  <section class="card space-y-4 p-5" data-testid="security-records">
    <div class="flex items-center justify-between gap-3">
      <h2 class="text-lg font-semibold">{{ t('securityRecords.title') }}</h2>
      <button class="btn btn-secondary btn-sm" :disabled="loading" @click="refresh">{{ t('common.refresh') }}</button>
    </div>
    <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
    <p v-if="loading" role="status">{{ t('common.loading') }}</p>
    <template v-if="!admin && summary">
      <p :class="summary.restricted ? 'font-medium text-red-600' : 'text-emerald-600'" data-testid="restriction-status">
        {{ summary.restricted ? t('securityRecords.restricted') : t('securityRecords.unrestricted') }}
      </p>
      <p class="text-sm text-gray-500">{{ t('securityRecords.loginUnaffected') }}</p>
      <p v-if="summary.observe_only && summary.enabled" class="text-sm">{{ t('securityRecords.observation') }}</p>
      <article v-for="state in summary.states" :key="state.scope + state.subject_key" class="rounded-lg border p-3 dark:border-dark-600">
        <p>{{ t(`securityRecords.scopes.${state.scope}`) }} · {{ t(`securityRecords.actions.${state.action}`) }} · {{ state.count }} / {{ summary.threshold }}</p>
        <p>{{ t('securityRecords.reason') }}: {{ state.reason || '—' }}</p>
        <p v-if="state.blocked_until || state.route_until">{{ t('securityRecords.until') }}: {{ date(state.blocked_until || state.route_until) }}</p>
        <p v-if="state.scope === 'session'" class="text-xs text-gray-500">{{ t('securityRecords.session') }}: {{ state.subject_key.slice(0, 12) }}</p>
      </article>
    </template>
    <p class="text-sm text-gray-500">{{ t('securityRecords.originalHint') }}</p>
    <p v-if="!loading && !events.items.length">{{ t('securityRecords.empty') }}</p>
    <article v-for="event in events.items" :key="event.id" class="space-y-2 rounded-lg border p-3 dark:border-dark-600">
      <p class="text-sm text-gray-500">#{{ event.id }} · {{ date(event.created_at) }} · {{ event.model }}<span v-if="admin"> · UID {{ event.user_id }}</span></p>
      <p>{{ t('securityRecords.reason') }}: {{ event.reason }}</p>
      <p class="text-sm">{{ t(`securityRecords.scopes.${event.scope}`) }} · {{ t('securityRecords.count') }}: {{ event.count }}</p>
      <button class="btn btn-secondary btn-sm" :disabled="opening === event.id" @click="open(event.id)">{{ expanded === event.id ? t('securityRecords.hidePrompt') : t('securityRecords.showPrompt') }}</button>
      <pre v-if="expanded === event.id && details[event.id]" class="max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-900" data-testid="security-original-prompt">{{ details[event.id]?.full_prompt }}</pre>
    </article>
    <div class="flex items-center gap-3">
      <button class="btn btn-secondary btn-sm" :disabled="loading || events.page <= 1" @click="loadPage(events.page - 1)">{{ t('securityRecords.previous') }}</button>
      <span>{{ events.page }} · {{ events.total }}</span>
      <button class="btn btn-secondary btn-sm" :disabled="loading || events.page * events.page_size >= events.total" @click="loadPage(events.page + 1)">{{ t('securityRecords.next') }}</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getEvent, getSummary, listEvents, type PenaltyEvent, type PenaltyPage, type PenaltySummary } from './api'
const props = withDefaults(defineProps<{ admin?: boolean; userId?: number }>(), { admin: false })
const { t } = useI18n()
const summary = ref<PenaltySummary | null>(null)
const events = ref<PenaltyPage>({ items: [], total: 0, page: 1, page_size: 10 })
const error = ref('')
const loading = ref(false)
const expanded = ref<number | null>(null)
const opening = ref<number | null>(null)
const details = ref<Record<number, PenaltyEvent>>({})
const date = (value: string | null) => value ? new Date(value).toLocaleString() : '—'
async function loadPage(page: number) {
  loading.value = true
  error.value = ''
  try { events.value = await listEvents(page, props.admin, props.userId) }
  catch { error.value = t('securityRecords.loadFailed') }
  finally { loading.value = false }
}
async function refresh() {
  details.value = {}
  expanded.value = null
  summary.value = null
  await loadPage(1)
  if (!props.admin) {
    try { summary.value = await getSummary() }
    catch { error.value = t('securityRecords.loadFailed') }
  }
}
async function open(id: number) {
  if (expanded.value === id) { expanded.value = null; return }
  opening.value = id
  try {
    if (!details.value[id]) details.value[id] = await getEvent(id, props.admin)
    expanded.value = id
  } catch { error.value = t('securityRecords.loadFailed') }
  finally { opening.value = null }
}
onMounted(refresh)
watch(() => props.userId, refresh)
defineExpose({ refresh })
</script>
