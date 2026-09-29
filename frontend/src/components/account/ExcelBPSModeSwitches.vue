<template>
  <div class="mt-3 space-y-3" :data-testid="prefix + '-modes'">
    <div class="grid gap-3 sm:grid-cols-2">
      <div v-for="choice in choices" :key="choice" class="rounded-xl border p-3 transition-colors" :class="enabled && mode === choice ? 'border-primary-300 bg-primary-50/70 dark:border-primary-700 dark:bg-primary-950/30' : 'border-gray-200 bg-gray-50/70 dark:border-dark-600 dark:bg-dark-800/40'">
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="text-sm font-medium">{{ t('autoConfig.bps.mode.' + choice) }}</p>
            <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('autoConfig.bps.mode.' + choice + 'Hint') }}</p>
          </div>
          <button type="button" role="switch" :aria-checked="enabled && mode === choice"
            :aria-label="t('autoConfig.bps.mode.' + choice)" :disabled="loading || choice === 'defaults' && !available && !(enabled && mode === choice)"
            :data-testid="choice === 'initial' ? prefix + '-toggle' : prefix + '-defaults-toggle'" :data-mode="choice"
            class="relative inline-flex h-6 w-11 shrink-0 rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
            :class="enabled && mode === choice ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'" @click="$emit('toggle', choice)">
            <span class="pointer-events-none inline-block h-5 w-5 rounded-full bg-white shadow transition-transform" :class="enabled && mode === choice ? 'translate-x-5' : 'translate-x-0'" />
          </button>
        </div>
      </div>
    </div>
    <div class="flex flex-wrap items-start justify-between gap-2 text-xs leading-5">
      <p class="text-gray-500 dark:text-gray-400">{{ t('autoConfig.bps.modeHint') }}</p>
      <a v-if="available" href="/admin/auto-config" target="_blank" rel="noopener noreferrer" class="shrink-0 text-primary-600 hover:underline dark:text-primary-400">{{ t('autoConfig.bps.manage') }} ↗</a>
    </div>
    <p v-if="loading" role="status" class="text-xs text-gray-500">{{ t('autoConfig.bps.applying') }}</p>
    <p v-else-if="failed" role="alert" class="text-xs text-red-600 dark:text-red-400">{{ t('autoConfig.bps.loadFailed') }}</p>
    <p v-else-if="applied && enabled" role="status" class="text-xs text-primary-700 dark:text-primary-300">{{ t('autoConfig.bps.mode.' + mode + 'Applied') }}</p>
    <p v-if="!available" class="text-xs text-gray-500">{{ t('autoConfig.bps.adminOnly') }}</p>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { ExcelBPSMode } from '@/utils/excelBPSDefaults'
withDefaults(defineProps<{ enabled: boolean; mode: ExcelBPSMode; loading: boolean; failed: boolean; applied: boolean; available?: boolean; prefix?: string }>(), { available: true, prefix: 'excel-bps' })
defineEmits<{ toggle: [mode: ExcelBPSMode] }>()
const { t } = useI18n()
const choices = ['defaults', 'initial'] as const
</script>
