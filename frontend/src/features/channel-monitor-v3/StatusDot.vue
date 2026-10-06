<template>
  <svg
    :class="['shrink-0', sizeClass]"
    viewBox="0 0 16 16"
    role="img"
    :aria-label="label"
    data-testid="monitor-v3-status-dot"
    :data-status="status"
  >
    <circle cx="8" cy="8" r="8" :class="fill" />
    <path v-if="status === 'operational'" d="M4.6 8.3 7 10.6l4.4-4.9" fill="none" stroke="white" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" />
    <g v-else-if="status === 'degraded'" fill="white">
      <rect x="7.1" y="3.6" width="1.8" height="5.6" rx="0.9" />
      <circle cx="8" cy="11.6" r="1.05" />
    </g>
    <path v-else-if="status === 'down'" d="m5.4 5.4 5.2 5.2m0-5.2-5.2 5.2" fill="none" stroke="white" stroke-width="1.8" stroke-linecap="round" />
    <path v-else d="M5.2 8h5.6" stroke="white" stroke-width="1.8" stroke-linecap="round" />
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { MonitorV3CellStatus, MonitorV3Status } from '@/api/channelMonitorV3'

const props = withDefaults(defineProps<{ status: MonitorV3Status | MonitorV3CellStatus; label?: string; size?: 'sm' | 'md' }>(), { label: '', size: 'sm' })

const sizeClass = computed(() => (props.size === 'md' ? 'h-4 w-4' : 'h-3.5 w-3.5'))
const fill = computed(() => ({
  operational: 'fill-[#22c39b]',
  degraded: 'fill-[#f6b928]',
  down: 'fill-[#f2715a]',
  unknown: 'fill-gray-300 dark:fill-dark-500',
  insufficient: 'fill-gray-300 dark:fill-dark-500',
}[props.status]))
</script>
