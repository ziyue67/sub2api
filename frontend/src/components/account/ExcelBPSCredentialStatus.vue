<template>
  <div v-if="state" class="space-y-1 text-xs" data-testid="excel-credential-state" aria-live="polite">
    <span :class="failed ? 'text-red-600 dark:text-red-400' : 'text-gray-500 dark:text-gray-400'">{{ t(`admin.accounts.bpsCredentialState.${status}`) }}</span>
    <p v-if="state.expires_at" class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.bpsCredentialState.expiresAt') }}: {{ formatDateTime(state.expires_at) }}</p>
    <p v-if="state.error_code" class="font-mono text-gray-500 dark:text-gray-400">{{ state.error_code }}</p>
    <p v-if="state.requires_manual_resume" class="text-amber-600 dark:text-amber-400">{{ t('admin.accounts.bpsCredentialState.manualResume') }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ExcelBPSCredentialState } from '@/types'
import { formatDateTime } from '@/utils/format'
import { excelCredentialStatus, useExcelCredentialClock } from '@/utils/excelBpsCredentialState'
const props = defineProps<{ state?: ExcelBPSCredentialState | null }>()
const { t } = useI18n()
const now = useExcelCredentialClock()
const status = computed(() => props.state ? excelCredentialStatus(props.state, now.value) : 'unknown')
const failed = computed(() => ['expired', 'revoked', 'auth_failed'].includes(status.value))
</script>
