<template>
  <div class="mt-2 max-w-72 text-xs leading-5" data-testid="credential-operation-failure">
    <p class="font-medium text-red-700 dark:text-red-300">{{ t(`tokenGuardV2.operationFailure.reasons.${safe.category}`) }}</p>
    <p class="text-gray-500 dark:text-gray-400">{{ t(`tokenGuardV2.operationFailure.actions.${safe.action}`) }}</p>
    <details class="mt-1 text-gray-500 dark:text-gray-400">
      <summary class="cursor-pointer">{{ t('tokenGuardV2.operationFailure.details') }}</summary>
      <p>{{ t('tokenGuardV2.operationFailure.code') }}: {{ safe.code }}</p>
      <p>{{ t('tokenGuardV2.operationFailure.stage') }}: {{ safe.stage }}</p>
      <p>{{ t('tokenGuardV2.operationFailure.time') }}: {{ time }}</p>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialOperationFailure } from '@/api/admin/accountTokenGuardV2'
import { safeOperationFailure } from '@/utils/credentialOperationFailure'

const props = defineProps<{ failure: CredentialOperationFailure }>()
const { t } = useI18n()
const safe = computed(() => safeOperationFailure(props.failure))
const time = computed(() => safe.value.occurred_at
  ? new Date(safe.value.occurred_at).toLocaleString()
  : t('tokenGuardV2.operationFailure.unknownTime'))
</script>
