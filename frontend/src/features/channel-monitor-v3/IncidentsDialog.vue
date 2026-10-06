<template>
  <BaseDialog :show="show" :title="t('channelMonitorV3.incidents.title')" width="wide" close-on-click-outside @close="emit('close')">
    <div class="space-y-3" data-testid="monitor-v3-incident-list">
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.incidents.hint', { since: since ? formatMonitorV3Full(since) : '—' }) }}</p>
      <div v-if="loading && !items.length" class="space-y-2">
        <div v-for="index in 3" :key="index" class="h-16 animate-pulse rounded-xl bg-gray-100 dark:bg-dark-700" />
      </div>
      <p v-else-if="error" class="rounded-xl bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <p v-else-if="!items.length" class="py-10 text-center text-sm text-gray-500 dark:text-gray-400" data-testid="monitor-v3-incidents-empty">
        {{ t('channelMonitorV3.incidents.empty') }}
      </p>
      <ul v-else class="divide-y divide-gray-100 overflow-hidden rounded-xl border border-gray-200 dark:divide-dark-700 dark:border-dark-700">
        <li v-for="item in items" :key="`${item.component_id}-${item.started_at}`" class="px-4 py-3" data-testid="monitor-v3-incident">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="flex min-w-0 items-center gap-2">
              <StatusDot :status="item.ended_at ? 'operational' : 'down'" />
              <span class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ item.component_name }}</span>
              <span
                class="rounded-full px-2 py-0.5 text-[10px] font-semibold"
                :class="item.ended_at ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-300'"
              >
                {{ t(item.ended_at ? 'channelMonitorV3.incidents.resolved' : 'channelMonitorV3.incidents.ongoing') }}
              </span>
            </div>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ duration(item) }}</span>
          </div>
          <p class="mt-1 text-xs text-gray-600 dark:text-gray-300">
            {{ reason(item.top_error) }} · {{ t('channelMonitorV3.incidents.downSlots', { count: item.down_slots }) }}
            <template v-if="item.requests"> · {{ t('channelMonitorV3.incidents.volume', { requests: item.requests, errors: item.errors || 0 }) }}</template>
          </p>
          <p class="mt-0.5 text-[11px] text-gray-400 dark:text-gray-500">
            {{ formatMonitorV3Full(item.started_at) }} → {{ item.ended_at ? formatMonitorV3Full(item.ended_at) : t('channelMonitorV3.incidents.now') }}
          </p>
        </li>
      </ul>
    </div>
    <template #footer>
      <div class="flex w-full items-center justify-between gap-3 text-xs text-gray-500 dark:text-gray-400">
        <span>{{ t('channelMonitorV3.incidents.total', { count: total }) }}</span>
        <div class="flex items-center gap-2">
          <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || page <= 1" @click="load(page - 1)">{{ t('channelMonitorV3.incidents.prev') }}</button>
          <span class="tabular-nums">{{ page }} / {{ pages }}</span>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || page >= pages" @click="load(page + 1)">{{ t('channelMonitorV3.incidents.next') }}</button>
        </div>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { getIncidents, type MonitorV3Incident } from '@/api/channelMonitorV3'
import { extractApiErrorMessage } from '@/utils/apiError'
import StatusDot from './StatusDot.vue'
import { formatMonitorV3Full } from './monitorV3'

const PAGE_SIZE = 20
const props = defineProps<{ show: boolean; admin: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()
const { t, te } = useI18n()
const items = ref<MonitorV3Incident[]>([])
const since = ref('')
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const error = ref('')
const pages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)))
let controller: AbortController | undefined

async function load(next: number) {
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  error.value = ''
  try {
    const result = await getIncidents(next, PAGE_SIZE, props.admin, current.signal)
    if (current.signal.aborted) return
    items.value = result.items || []
    since.value = result.since
    total.value = result.total
    page.value = result.page
  } catch (err: unknown) {
    if (current.signal.aborted) return
    error.value = extractApiErrorMessage(err, t('channelMonitorV3.incidents.loadFailed'))
  } finally {
    if (controller === current) loading.value = false
  }
}

function reason(category?: string) {
  const key = `channelMonitorV3.errors.${category || 'other'}`
  return te(key) ? t(key) : t('channelMonitorV3.errors.other')
}

function duration(item: MonitorV3Incident) {
  const end = item.ended_at ? new Date(item.ended_at).getTime() : Date.now()
  const minutes = Math.max(0, Math.round((end - new Date(item.started_at).getTime()) / 60000))
  if (minutes < 60) return t('channelMonitorV3.incidents.minutes', { count: minutes })
  const hours = Math.floor(minutes / 60)
  return t('channelMonitorV3.incidents.hours', { hours, minutes: minutes % 60 })
}

watch(() => props.show, (open) => {
  if (open) void load(1)
  else controller?.abort()
}, { immediate: true })
</script>
