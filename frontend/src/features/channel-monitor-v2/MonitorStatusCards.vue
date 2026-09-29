<template>
  <div class="space-y-7" data-testid="monitor-status-cards">
    <div v-if="loading && !items.length" class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
      <div v-for="index in 6" :key="index" class="h-72 animate-pulse rounded-3xl bg-gray-100 dark:bg-dark-800" />
    </div>
    <div v-else-if="!items.length" class="card py-16 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.empty.title') }}</div>
    <section v-for="platform in platforms" :key="platform.name" class="space-y-3">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-gray-700 dark:text-gray-200">
        <span class="grid h-7 w-7 place-items-center rounded-full bg-gray-100 dark:bg-dark-700"><ProviderIcon :provider="platform.name" :size="17" /></span>
        {{ providerLabel(platform.name) }}
        <span class="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-dark-700 dark:text-gray-400">{{ platform.items.length }}</span>
      </h2>
      <div class="grid grid-cols-1 items-start gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
        <article v-for="row in platform.items" :key="`${row.platform}-${row.group_id}`" class="min-w-0 rounded-3xl border border-gray-200/80 bg-white/80 p-5 shadow-sm dark:border-dark-700/70 dark:bg-dark-800/80" data-testid="monitor-group-card">
          <header class="flex items-start gap-3">
            <span class="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-900/20 dark:text-primary-300"><ProviderIcon :provider="row.platform" :size="23" /></span>
            <div class="min-w-0 flex-1">
              <h3 class="truncate text-base font-semibold text-gray-900 dark:text-gray-100" :title="row.group_name">{{ row.group_name || `#${row.group_id}` }}</h3>
              <div class="mt-1 flex flex-wrap gap-1 text-[10px]">
                <span class="rounded px-1.5 py-0.5" :class="providerBadgeClass(row.platform)">{{ providerLabel(row.platform) }}</span>
                <span v-if="row.group_rate_multiplier != null" class="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-gray-500 dark:bg-dark-700 dark:text-gray-400">{{ t('channelMonitorV2.cards.groupRate', { value: row.group_rate_multiplier.toFixed(2) }) }}</span>
              </div>
            </div>
            <span class="shrink-0 rounded-full px-2 py-1 text-[10px] font-semibold" :class="healthBadge[row.health.overall]">{{ t(`channelMonitorV2.cards.health.${row.health.overall}`) }}</span>
          </header>

          <dl class="mt-5 grid grid-cols-3 gap-2">
            <div v-for="metric in metrics(row)" :key="metric.name" class="min-w-0 rounded-2xl border border-gray-100 bg-gray-50/70 px-2.5 py-3 dark:border-dark-700/60 dark:bg-dark-900/40">
              <dt class="truncate text-[10px] tracking-wide text-gray-500 dark:text-gray-400" :title="metric.title || metric.name">{{ metric.name }}</dt>
              <dd class="mt-1.5 font-mono text-base font-semibold tabular-nums" :class="metric.color">{{ metric.value }}</dd>
            </div>
          </dl>
          <div class="mt-4 border-t border-gray-100 pt-3 dark:border-dark-700/70">
            <div class="mb-2 flex justify-between text-[10px] text-gray-500 dark:text-gray-400">
              <span>{{ t('channelMonitorV2.cards.windows', { count: 18 }) }}</span><span>{{ t('channelMonitorV2.cards.refreshIn', { seconds: countdown }) }}</span>
            </div>
            <div class="flex h-5 items-end gap-1" :aria-label="t('channelMonitorV2.cards.trafficHistory')">
              <button v-for="bar in monitorCardTimeline(row, coverage)" :key="bar.start" type="button"
                class="min-w-0 flex-1 cursor-help rounded-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-400"
                :class="healthBar[bar.state]" :aria-label="trafficLabel(row, bar)" :aria-describedby="isTrafficDetail(row, bar) ? tooltipId : undefined"
                data-testid="traffic-history-bar" @mouseenter="openDetail($event, { kind: 'traffic', row, bar })"
                @focus="openDetail($event, { kind: 'traffic', row, bar })" @click="openDetail($event, { kind: 'traffic', row, bar })"
                @mouseleave="scheduleClose" @blur="scheduleClose" />
            </div>
            <div class="mt-1 flex justify-between font-mono text-[9px] tracking-widest text-gray-400"><span>PAST</span><span>NOW</span></div>
          </div>

          <section class="mt-4 border-t border-gray-100 pt-3 dark:border-dark-700/70" data-testid="candy-history">
            <template v-if="row.candy">
              <div class="flex items-center justify-between gap-2 text-xs">
                <h4 class="font-medium text-gray-700 dark:text-gray-200">{{ t('channelMonitorV2.candy.title') }}</h4>
                <span class="flex items-center gap-1.5 text-[10px] text-gray-500 dark:text-gray-400"><i class="h-2 w-2 rounded-full" :class="candyColor[candyDisplayState(row.candy, now)]" />{{ t(`channelMonitorV2.candy.states.${candyDisplayState(row.candy, now)}`) }}</span>
              </div>
              <p class="mt-1 break-words text-[10px] leading-relaxed text-gray-500 dark:text-gray-400">{{ row.candy.model }} · {{ row.candy.reasoning_effort }} · {{ t('channelMonitorV2.candy.cadence', { minutes: row.candy.interval_minutes }) }} · {{ t('channelMonitorV2.candy.window') }}</p>
              <div v-if="row.candy.results.length" class="mt-2 flex h-5 gap-px">
                <button v-for="result in row.candy.results" :key="result.checked_at" type="button"
                  class="min-w-0 flex-1 cursor-help rounded-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-400"
                  :class="candyColor[result.verdict]" :aria-label="candyTooltip(result)" :aria-describedby="isCandyDetail(row, result) ? tooltipId : undefined"
                  data-testid="candy-history-bar" @mouseenter="openDetail($event, { kind: 'candy', row, result })"
                  @focus="openDetail($event, { kind: 'candy', row, result })" @click="openDetail($event, { kind: 'candy', row, result })"
                  @mouseleave="scheduleClose" @blur="scheduleClose" />
              </div>
              <p v-else class="mt-3 text-[10px] text-gray-400">{{ t('channelMonitorV2.candy.waiting') }}</p>
              <div class="mt-2 flex items-center justify-between gap-2 text-[9px] text-gray-500 dark:text-gray-400"><span>{{ t('channelMonitorV2.candy.legend') }}</span><span>{{ row.candy.results.length ? time(row.candy.results.at(-1)!.checked_at) : '—' }}</span></div>
            </template>
            <template v-else>
              <div class="flex items-center justify-between gap-2 text-xs">
                <h4 class="font-medium text-gray-700 dark:text-gray-200">{{ t('channelMonitorV2.candy.title') }}</h4>
                <span class="flex items-center gap-1.5 text-[10px] text-gray-500 dark:text-gray-400"><i class="h-2 w-2 rounded-full bg-gray-400" />{{ t('channelMonitorV2.candy.disabled') }}</span>
              </div>
              <p class="mt-2 text-[10px] leading-relaxed text-gray-500 dark:text-gray-400" data-testid="candy-not-configured">{{ t('channelMonitorV2.candy.disabledHint') }}</p>
            </template>
          </section>
        </article>
      </div>
    </section>
    <Teleport to="body">
      <div v-if="detail" :id="tooltipId" ref="tooltipElement" role="tooltip" tabindex="0"
        class="monitor-card-tooltip fixed z-[100] w-max max-w-[calc(100vw-24px)] rounded-xl border border-gray-300 bg-white px-3 py-2 text-xs leading-relaxed text-gray-800 shadow-xl dark:border-gray-400 dark:bg-dark-900 dark:text-gray-100"
        :style="{ left: placement.left + 'px', top: placement.top + 'px', visibility: placement.ready ? 'visible' : 'hidden' }"
        @mouseenter="cancelClose" @mouseleave="scheduleClose" @focus="cancelClose" @blur="scheduleClose">
        <span aria-hidden="true" class="absolute h-2.5 w-2.5 rotate-45 border-gray-300 bg-white dark:border-gray-400 dark:bg-dark-900"
          :class="placement.below ? '-top-1.5 border-l border-t' : '-bottom-1.5 border-b border-r'" :style="{ left: placement.arrowLeft + 'px' }" />
        <div class="max-h-[min(18rem,calc(100vh-48px))] max-w-lg overflow-y-auto overscroll-contain break-words" data-testid="monitor-card-tooltip-content">
          <p class="font-semibold">{{ detail.row.group_name || '#' + detail.row.group_id }}</p>
          <template v-if="detail.kind === 'traffic'">
            <p class="text-gray-500 dark:text-gray-400">{{ dateTime(detail.bar.start) }} - {{ dateTime(detail.bar.end) }} · {{ t('channelMonitorV2.cards.health.' + detail.bar.state) }}</p>
            <p v-if="!detail.bar.buckets.length" class="mt-1">{{ t('channelMonitorV2.matrix.noTraffic') }}</p>
            <template v-else>
              <p class="mt-1 text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.cards.bucketDetails') }}</p>
              <p v-for="bucket in detail.bar.buckets" :key="bucket.bucket_start" class="mt-1" data-testid="monitor-card-tooltip-sample">{{ trafficSample(bucket) }}</p>
            </template>
          </template>
          <p v-else class="mt-1 whitespace-pre-wrap">{{ candyTooltip(detail.result) }}</p>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorCandyResult, MonitorCoverage, MonitorMatrixBucket, MonitorMatrixRow } from '@/api/channelMonitorV2'
import ProviderIcon from '@/components/user/monitor/ProviderIcon.vue'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import { formatMonitorMs, formatMonitorPercent } from './monitorFormat'
import { candyDisplayState, hasMonitorSamples, monitorCardTimeline } from './monitorCards'

const props = defineProps<{ items: MonitorMatrixRow[]; coverage?: MonitorCoverage; countdown: number; loading: boolean; now: number }>()
const { t, locale } = useI18n()
type TrafficBar = ReturnType<typeof monitorCardTimeline>[number]
type CardDetail = { kind: 'traffic'; row: MonitorMatrixRow; bar: TrafficBar } | { kind: 'candy'; row: MonitorMatrixRow; result: MonitorCandyResult }
const tooltipId = useId()
const detail = shallowRef<CardDetail | null>(null)
const anchor = shallowRef<HTMLElement | null>(null)
const tooltipElement = ref<HTMLElement | null>(null)
const placement = ref({ left: 0, top: 0, arrowLeft: 0, below: false, ready: false })
let closeTimer: ReturnType<typeof setTimeout> | undefined

function cancelClose() {
  if (closeTimer) clearTimeout(closeTimer)
  closeTimer = undefined
}
function closeDetail() {
  cancelClose()
  detail.value = null
  anchor.value = null
}
function scheduleClose() {
  cancelClose()
  // Allow the pointer to cross the arrow gap and scroll multi-bucket details.
  closeTimer = setTimeout(() => {
    if (document.activeElement !== anchor.value && !tooltipElement.value?.contains(document.activeElement)) closeDetail()
  }, 120)
}
function openDetail(event: Event, value: CardDetail) {
  cancelClose()
  anchor.value = event.currentTarget as HTMLElement
  detail.value = value
  placement.value.ready = false
  void nextTick(positionDetail)
}
function positionDetail() {
  if (!anchor.value || !tooltipElement.value || !detail.value) return
  const rect = anchor.value.getBoundingClientRect()
  const box = tooltipElement.value.getBoundingClientRect()
  const center = rect.left + rect.width / 2
  const left = Math.max(12, Math.min(window.innerWidth - box.width - 12, center - box.width / 2))
  const below = rect.top < box.height + 22
  const top = Math.max(12, Math.min(window.innerHeight - box.height - 12, below ? rect.bottom + 10 : rect.top - box.height - 10))
  placement.value = { left, top, below, ready: true, arrowLeft: Math.max(8, Math.min(box.width - 18, center - left - 5)) }
}
function onOutsidePointer(event: Event) {
  if (!(event.target instanceof Node)) return
  if (!anchor.value?.contains(event.target) && !tooltipElement.value?.contains(event.target)) closeDetail()
}
function onViewportChange(event: Event) {
  if (event.target instanceof Node && tooltipElement.value?.contains(event.target)) return
  closeDetail()
}
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') closeDetail()
}
function isTrafficDetail(row: MonitorMatrixRow, bar: TrafficBar) {
  return detail.value?.kind === 'traffic' && detail.value.row === row && detail.value.bar.start === bar.start
}
function isCandyDetail(row: MonitorMatrixRow, result: MonitorCandyResult) {
  return detail.value?.kind === 'candy' && detail.value.row === row && detail.value.result.checked_at === result.checked_at
}
watch([() => props.items, () => props.coverage], closeDetail)
onMounted(() => {
  document.addEventListener('pointerdown', onOutsidePointer, true)
  document.addEventListener('keydown', onKeydown)
  window.addEventListener('resize', onViewportChange)
  window.addEventListener('scroll', onViewportChange, true)
})
onBeforeUnmount(() => {
  cancelClose()
  document.removeEventListener('pointerdown', onOutsidePointer, true)
  document.removeEventListener('keydown', onKeydown)
  window.removeEventListener('resize', onViewportChange)
  window.removeEventListener('scroll', onViewportChange, true)
})
const { providerLabel, providerBadgeClass } = useChannelMonitorFormat()
const platforms = computed(() => {
  const groups = new Map<string, MonitorMatrixRow[]>()
  for (const row of props.items) {
    if (!row.group_id) continue
    if (!groups.has(row.platform)) groups.set(row.platform, [])
    groups.get(row.platform)!.push(row)
  }
  const order = ['openai', 'anthropic', 'grok', 'gemini', 'antigravity']
  return [...groups].map(([name, items]) => ({ name, items })).sort((a, b) => (order.indexOf(a.name) < 0 ? 99 : order.indexOf(a.name)) - (order.indexOf(b.name) < 0 ? 99 : order.indexOf(b.name)))
})
const healthBadge = { healthy: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300', warning: 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300', critical: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300', unknown: 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400' }
const healthBar = { healthy: 'h-5 bg-emerald-400', warning: 'h-3 bg-amber-400', critical: 'h-2 bg-red-400', unknown: 'h-1 bg-gray-300 dark:bg-dark-600' }
const candyColor = { correct: 'bg-emerald-400', incorrect: 'bg-amber-400', error: 'bg-red-400', unknown: 'bg-gray-400', stale: 'bg-gray-400' }
const time = (value: number | string) => new Date(value).toLocaleTimeString(locale.value, { hour: '2-digit', minute: '2-digit', hour12: false })
function metrics(row: MonitorMatrixRow) {
  const sampled = hasMonitorSamples(row.metrics)
  return [
    { name: t('channelMonitorV2.cards.cache'), value: sampled ? formatMonitorPercent(row.metrics.cache_rate) : '—', color: 'text-gray-800 dark:text-gray-100' },
    { name: t('channelMonitorV2.cards.availability'), value: sampled ? formatMonitorPercent(1 - row.metrics.error_rate) : '—', color: sampled && row.health.error_rate === 'healthy' ? 'text-emerald-600 dark:text-emerald-300' : 'text-gray-800 dark:text-gray-100' },
    { name: t('channelMonitorV2.cards.ttft'), title: t('channelMonitorV2.metrics.ttftP50'), value: formatMonitorMs(row.metrics.ttft.p50_ms), color: 'text-gray-800 dark:text-gray-100' }
  ]
}
function dateTime(value: number | string) {
  return new Date(value).toLocaleString(locale.value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
}
function trafficLabel(row: MonitorMatrixRow, bar: TrafficBar) {
  return (row.group_name || '#' + row.group_id) + ' · ' + dateTime(bar.start) + ' - ' + dateTime(bar.end) + ' · ' + t('channelMonitorV2.cards.health.' + bar.state)
}
function trafficSample(bucket: MonitorMatrixBucket) {
  return t('channelMonitorV2.cards.sampleDetails', {
    time: dateTime(bucket.bucket_start), availability: formatMonitorPercent(1 - bucket.metrics.error_rate),
    cache: formatMonitorPercent(bucket.metrics.cache_rate), ttft: formatMonitorMs(bucket.metrics.ttft.p50_ms),
  })
}
function candyTooltip(result: MonitorCandyResult) {
  return dateTime(result.checked_at) + ' · ' + t('channelMonitorV2.candy.states.' + result.verdict)
    + ' · ' + t('channelMonitorV2.metrics.durationValue', { value: formatMonitorMs(result.latency_ms) })
    + (result.answer_preview ? '\n' + result.answer_preview : '')
}
</script>
