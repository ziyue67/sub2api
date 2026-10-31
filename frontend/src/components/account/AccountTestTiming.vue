<template>
  <section v-if="delivery !== 'idle'" data-testid="account-test-timing" class="min-w-0 rounded-lg border border-gray-200 p-3 text-xs dark:border-dark-500" aria-live="polite">
    <dl class="grid grid-cols-1 gap-2 sm:grid-cols-2">
      <div class="min-w-0"><dt class="text-gray-500">{{ t('accountTestTiming.started') }}</dt><dd class="break-words">{{ timestamp(timing?.started_at) }}</dd></div>
      <div class="min-w-0"><dt class="text-gray-500">{{ t('accountTestTiming.finished') }}</dt><dd class="break-words">{{ timestamp(timing?.finished_at) }}</dd></div>
      <div><dt class="text-gray-500">{{ t('accountTestTiming.total') }}</dt><dd>{{ duration(timing?.total_ms) }}</dd></div>
      <div><dt class="text-gray-500">{{ t('accountTestTiming.first_content') }}</dt><dd>{{ duration(timing?.first_content?.duration_ms) }}<span v-if="!isDuration(timing?.first_content?.duration_ms)" class="ml-1 text-gray-500">{{ t(`accountTestTiming.reasons.${reason('first_content')}`) }}</span></dd></div>
    </dl>
    <p class="mt-2 break-words">{{ t(`accountTestTiming.delivery.${delivery}`) }}<span v-if="timing?.finished_at"> · {{ t(`accountTestTiming.outcome.${safeOutcome}`) }}</span></p>
    <p v-if="delivery !== 'finished' && delivery !== 'running'" class="mt-1">{{ t('accountTestTiming.missingFinal') }}</p>
    <details class="mt-2">
      <summary class="cursor-pointer">{{ t('accountTestTiming.details') }}</summary>
      <p class="my-2 text-gray-500">{{ t('accountTestTiming.overlap') }}</p>
      <dl class="space-y-3">
        <div v-for="key in metrics" :key="key" :data-metric="key" class="min-w-0">
          <dt class="font-medium">{{ t(`accountTestTiming.${key}`) }} · {{ duration(timing?.[key]?.duration_ms) }}</dt>
          <dd class="break-words text-gray-500">{{ t(`accountTestTiming.boundaries.${key}`) }}</dd>
          <dd v-if="isDuration(timing?.[key]?.duration_ms)">
            {{ t('accountTestTiming.range', { start: duration(timing?.[key]?.start_ms), end: duration(timing?.[key]?.end_ms) }) }}
            <span v-if="key === 'local_processing'"> · {{ t('accountTestTiming.segments', { count: timing?.[key]?.segments || 0 }) }}</span>
          </dd>
          <dd v-else>{{ t(`accountTestTiming.reasons.${reason(key)}`) }}</dd>
        </div>
      </dl>
    </details>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountTestDelivery, AccountTestTiming } from '@/composables/useAccountTestTiming'

const props = defineProps<{ timing: AccountTestTiming | null; delivery: AccountTestDelivery }>()
const { t, locale } = useI18n()
const metrics = ['connection', 'response_headers', 'first_content', 'stream_output', 'local_processing'] as const
const reasons = ['not_observed', 'no_content', 'not_isolated', 'invalid_observation', 'connection_reused', 'multiple_requests', 'non_stream', 'unsupported_path', 'collector_limit']
const safeOutcome = computed(() => ['success', 'error', 'timeout', 'canceled'].includes(props.timing?.outcome || '') ? props.timing!.outcome : 'error')
const isDuration = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value) && value >= 0
const duration = (value: unknown) => isDuration(value) ? `${new Intl.NumberFormat(locale.value, { maximumFractionDigits: 1 }).format(value)} ms` : '—'
function timestamp(value?: string) {
  if (!value || !Number.isFinite(Date.parse(value))) return '—'
  const date = new Date(value)
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
  const offset = -date.getTimezoneOffset()
  const suffix = `UTC${offset >= 0 ? '+' : '-'}${String(Math.floor(Math.abs(offset) / 60)).padStart(2, '0')}:${String(Math.abs(offset) % 60).padStart(2, '0')}`
  return `${new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(date)} (${zone}, ${suffix})`
}
function reason(key: typeof metrics[number]) {
  if (!props.timing) return 'not_observed'
  const value = props.timing[key]?.reason
  return reasons.includes(value || '') ? value : 'not_observed'
}
</script>
