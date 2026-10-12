<template>
  <div class="my-3 border-t border-gray-100 pt-2 text-xs dark:border-dark-700" data-testid="account-coverage">
    <p class="font-semibold text-gray-900 dark:text-white">{{ t('admin.harvestFlow.coverageAccountTitle') }} · {{ coverage?.percent != null ? `${coverage.percent.toFixed(1)}%` : '—' }}</p>
    <p v-if="coverage?.percent != null" class="mt-1 text-gray-500 dark:text-gray-400">{{ t('admin.harvestFlow.coverageAccountDurations', { ready: duration(coverage.ready_seconds), missing: duration(coverage.missing_seconds) }) }}</p>
    <p v-else class="mt-1 text-gray-400">{{ t('admin.harvestFlow.coverageWaiting') }}</p>
    <p v-if="coverage" class="mt-1 text-gray-400">{{ t('admin.harvestFlow.coverageAccountSince', { time: new Date(coverage.started_at).toLocaleString() }) }}</p>
    <p v-if="coverage && Date.parse(generatedAt) - Date.parse(coverage.updated_at) > 15000" class="mt-1 text-amber-600">{{ t('admin.harvestFlow.coverageStale') }}</p>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { CodexHarvestFlowAccount } from '@/api/admin/accounts'
defineProps<{ coverage?: CodexHarvestFlowAccount['coverage']; generatedAt: string }>()
const { t } = useI18n()
function duration(seconds: number) {
  return seconds < 60 ? `${Math.floor(seconds)}s` : `${(seconds / 60).toFixed(1)}min`
}
</script>
