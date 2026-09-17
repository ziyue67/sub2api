<template>
  <div v-if="eligible" class="flex min-h-6 min-w-[8rem] items-center gap-1">
    <span
      class="max-w-48 truncate whitespace-nowrap text-sm font-medium"
      :class="valueClass"
      data-testid="upstream-usage-value"
      :title="detailTitle"
    >
      {{ primaryValue }}
    </span>
    <button
      type="button"
      class="inline-flex h-6 w-6 flex-shrink-0 items-center justify-center rounded text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
      :disabled="probing"
      :aria-label="t('admin.accounts.upstreamUsage.refresh')"
      :title="t('admin.accounts.upstreamUsage.refresh')"
      data-testid="upstream-usage-probe"
      @click="$emit('probe')"
    >
      <Icon name="refresh" size="xs" :class="{ 'animate-spin': probing }" />
    </button>
  </div>
  <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { Account, UpstreamUsageProbeSnapshot } from '@/types'

const props = withDefaults(defineProps<{
  account: Account
  now: number
  probing?: boolean
}>(), {
  probing: false
})

defineEmits<{
  (event: 'probe'): void
}>()

const { t } = useI18n()
const CLOCK_SKEW_TOLERANCE_MS = 5 * 60 * 1000
const eligible = computed(() => props.account.type === 'apikey')
const snapshot = computed<UpstreamUsageProbeSnapshot | undefined>(() => props.account.extra?.upstream_usage_probe)
const data = computed(() => snapshot.value?.data)
const amount = computed(() => {
  const candidate = data.value?.remaining ?? data.value?.balance ?? data.value?.quota?.remaining
  return typeof candidate === 'number' && Number.isFinite(candidate) ? candidate : null
})
const unit = computed(() => data.value?.unit || data.value?.quota?.unit || '')
const fetchedAt = computed(() => {
  const value = snapshot.value?.fetched_at
  return typeof value === 'string' ? Date.parse(value) : Number.NaN
})
const freshUntil = computed(() => {
  const value = snapshot.value?.fresh_until
  return typeof value === 'string' ? Date.parse(value) : Number.NaN
})
const stale = computed(() => {
  if (!snapshot.value) return false
  if (snapshot.value.status !== 'ok') return false
  if (!Number.isFinite(fetchedAt.value) || fetchedAt.value > props.now + CLOCK_SKEW_TOLERANCE_MS) return true
  return !Number.isFinite(freshUntil.value) || props.now > freshUntil.value
})
const formatAmount = (value: number) => {
  const maximumFractionDigits = Math.abs(value) >= 1000 ? 2 : 6
  const formatted = new Intl.NumberFormat(undefined, { maximumFractionDigits }).format(value)
  return unit.value ? `${formatted} ${unit.value}` : formatted
}
const primaryValue = computed(() => {
  if (!snapshot.value) return '-'
  if (snapshot.value.status === 'unsupported') return t('admin.accounts.upstreamUsage.unsupported')
  if (snapshot.value.status === 'failed') return t('admin.accounts.upstreamUsage.failed')
  if (stale.value) return t('admin.accounts.upstreamUsage.stale')
  return amount.value == null ? t('admin.accounts.upstreamUsage.unavailable') : formatAmount(amount.value)
})
const valueClass = computed(() => {
  if (!snapshot.value) return 'text-gray-400 dark:text-gray-500'
  if (snapshot.value.status === 'failed') return 'text-red-600 dark:text-red-400'
  if (snapshot.value.status === 'unsupported' || amount.value == null) return 'text-gray-500 dark:text-gray-400'
  if (stale.value) return 'text-amber-600 dark:text-amber-400'
  return 'font-mono text-gray-800 dark:text-gray-200'
})
const formatDate = (value: number) => Number.isFinite(value)
  ? new Date(value).toLocaleString(undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
  : '-'
const detailTitle = computed(() => {
  if (!snapshot.value) return t('admin.accounts.upstreamUsage.notQueried')
  if (snapshot.value.status !== 'ok') return primaryValue.value
  return t('admin.accounts.upstreamUsage.updatedAt', { value: formatDate(fetchedAt.value) })
})
</script>
