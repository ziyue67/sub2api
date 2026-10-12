<template>
  <div class="card p-4" data-testid="harvest-coverage">
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.harvestFlow.coverageTitle') }}</p>
    <p class="mt-1 text-2xl font-bold text-gray-900 dark:text-white">{{ available ? `${coverage!.average_percent!.toFixed(1)}%` : '—' }}</p>
    <p v-if="available" class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.harvestFlow.coverageDurations', { ready: duration(coverage!.average_ready_seconds), missing: duration(coverage!.average_missing_seconds) }) }}</p>
    <p v-else class="text-xs text-gray-400">{{ t('admin.harvestFlow.coverageWaiting') }}</p>
    <p v-if="coverage?.started_at" class="mt-1 text-xs text-gray-400">{{ t('admin.harvestFlow.coverageSince', { time: new Date(coverage.started_at).toLocaleString(), n: coverage.account_count }) }}</p>
    <p v-if="stale" class="mt-1 text-xs text-amber-600">{{ t('admin.harvestFlow.coverageStale') }}</p>
    <p class="mt-2 text-xs text-gray-400">{{ t('admin.harvestFlow.coverageHint') }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CodexHarvestCoverage } from '@/api/admin/accounts'
const props = defineProps<{ coverage?: CodexHarvestCoverage; generatedAt: string }>()
const { t } = useI18n()
const available = computed(() => props.coverage?.average_percent != null && props.coverage.account_count > 0)
const stale = computed(() => props.coverage?.updated_at && Date.parse(props.generatedAt) - Date.parse(props.coverage.updated_at) > 15000)
function duration(seconds: number) {
  return seconds < 60 ? `${Math.floor(seconds)}s` : `${(seconds / 60).toFixed(1)}min`
}
</script>
