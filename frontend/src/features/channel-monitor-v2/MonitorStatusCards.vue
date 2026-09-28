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
              <span v-for="bar in monitorCardTimeline(row, coverage)" :key="bar.start" class="min-w-0 flex-1 rounded-sm" :class="healthBar[bar.state]" :title="`${time(bar.start)} - ${time(bar.end)}: ${t(`channelMonitorV2.cards.health.${bar.state}`)}`" />
            </div>
            <div class="mt-1 flex justify-between font-mono text-[9px] tracking-widest text-gray-400"><span>PAST</span><span>NOW</span></div>
          </div>

          <section v-if="row.candy" class="mt-4 border-t border-gray-100 pt-3 dark:border-dark-700/70" data-testid="candy-history">
            <div class="flex items-center justify-between gap-2 text-xs">
              <h4 class="font-medium text-gray-700 dark:text-gray-200">{{ t('channelMonitorV2.candy.title') }}</h4>
              <span class="flex items-center gap-1.5 text-[10px] text-gray-500 dark:text-gray-400"><i class="h-2 w-2 rounded-full" :class="candyColor[candyDisplayState(row.candy, now)]" />{{ t(`channelMonitorV2.candy.states.${candyDisplayState(row.candy, now)}`) }}</span>
            </div>
            <p class="mt-1 break-words text-[10px] leading-relaxed text-gray-500 dark:text-gray-400">{{ row.candy.model }} · {{ row.candy.reasoning_effort }} · {{ t('channelMonitorV2.candy.cadence', { minutes: row.candy.interval_minutes }) }} · {{ t('channelMonitorV2.candy.window') }}</p>
            <div v-if="row.candy.results.length" class="mt-2 flex h-5 gap-px">
              <span v-for="result in row.candy.results" :key="result.checked_at" tabindex="0" class="min-w-0 flex-1 rounded-sm focus:outline focus:outline-2 focus:outline-primary-400" :class="candyColor[result.verdict]" :title="candyTooltip(result)" :aria-label="candyTooltip(result)" />
            </div>
            <p v-else class="mt-3 text-[10px] text-gray-400">{{ t('channelMonitorV2.candy.waiting') }}</p>
            <div class="mt-2 flex items-center justify-between gap-2 text-[9px] text-gray-500 dark:text-gray-400"><span>{{ t('channelMonitorV2.candy.legend') }}</span><span>{{ row.candy.results.length ? time(row.candy.results.at(-1)!.checked_at) : '—' }}</span></div>
          </section>
        </article>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorCandyResult, MonitorCoverage, MonitorMatrixRow } from '@/api/channelMonitorV2'
import ProviderIcon from '@/components/user/monitor/ProviderIcon.vue'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import { formatMonitorMs, formatMonitorPercent } from './monitorFormat'
import { candyDisplayState, hasMonitorSamples, monitorCardTimeline } from './monitorCards'

const props = defineProps<{ items: MonitorMatrixRow[]; coverage?: MonitorCoverage; countdown: number; loading: boolean; now: number }>()
const { t, locale } = useI18n()
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
function candyTooltip(result: MonitorCandyResult) {
  return `${time(result.checked_at)} · ${t(`channelMonitorV2.candy.states.${result.verdict}`)}${result.answer_preview ? ` · ${result.answer_preview}` : ''}`
}
</script>
