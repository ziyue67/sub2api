<template>
  <span class="flex min-w-0 flex-1 flex-col gap-1 text-left">
    <span class="flex flex-wrap items-center gap-x-2 gap-y-1">
      <span class="min-w-0 flex-1 whitespace-normal break-words text-sm font-semibold text-gray-900 dark:text-gray-100">{{ group.label }}</span>
      <span v-if="effectiveRate !== undefined" class="shrink-0 whitespace-nowrap rounded-md bg-primary-100 px-2 py-1 text-sm font-bold text-primary-800 dark:bg-primary-900/50 dark:text-primary-200">
        {{ t('admin.groups.rateLabel') }} {{ effectiveRate }}x
      </span>
    </span>
    <span class="flex flex-wrap gap-x-2 text-xs text-gray-600 dark:text-gray-300">
      <span v-if="group.platform">{{ group.platform === 'composite' ? t('keys.routingCompositePlatform') : platformDisplayName(group.platform) }}</span>
      <span v-if="hasCustomRate">{{ t('keys.routingBaseRate') }} <s>{{ group.rate }}x</s></span>
      <span v-if="group.subscriptionType === 'subscription'">{{ t('groups.subscription') }}</span>
    </span>
    <span v-if="peakRate" class="whitespace-normal text-xs font-medium text-amber-700 dark:text-amber-300">{{ peakRate }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { platformDisplayName } from '@/constants/platformCatalog'
import { formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { useAppStore } from '@/stores/app'

export interface RoutingGroupChoice extends Record<string, unknown> {
  value: number
  label: string
  platform?: string
  rate?: number
  userRate?: number | null
  subscriptionType?: string
  peakRateEnabled?: boolean
  peakStart?: string
  peakEnd?: string
  peakRateMultiplier?: number
  disabled?: boolean
}
const props = defineProps<{ group: RoutingGroupChoice }>()
const { t } = useI18n()
const appStore = useAppStore()
const effectiveRate = computed(() => props.group.userRate ?? props.group.rate)
const hasCustomRate = computed(() => props.group.userRate != null && props.group.rate !== undefined && props.group.userRate !== props.group.rate)
const peakRate = computed(() => props.group.peakRateEnabled && props.group.peakStart && props.group.peakEnd
  ? formatPeakRateWindow({
    peak_rate_enabled: true,
    peak_start: props.group.peakStart,
    peak_end: props.group.peakEnd,
    peak_rate_multiplier: props.group.peakRateMultiplier
  }, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset)) : '')
</script>
