<template>
  <div v-if="state" class="mt-1 max-w-64 text-xs" :class="state.phase === 'isolated' || state.phase === 'isolation_pending' ? 'text-amber-600' : 'text-gray-500'" data-testid="admission-progress">
    <div v-if="state.phase === 'pending'">{{ t('autoConfig.admission.pending', { count: state.pass_streak, required: state.policy.pass_count }) }}</div>
    <div v-else-if="state.phase === 'passed'">{{ t('autoConfig.admission.passed') }}</div>
    <div v-else-if="state.phase === 'isolation_pending'">{{ t('autoConfig.admission.isolationPending') }}</div>
    <div v-else>{{ t('autoConfig.admission.isolated') }}</div>
    <div v-if="state.phase === 'pending' || state.phase === 'passed'">{{ t('autoConfig.admission.streak29', { count: state.isolation_streak, required: state.policy.isolation_count }) }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import type { OpenAITwoFAAdmissionConfig } from '@/utils/openaiTwoFAAdmission'
const props = defineProps<{ account: Account }>()
const { t } = useI18n()
const state = computed(() => {
  const s = props.account.extra?.openai_two_fa_admission as { phase: string; pass_streak: number; isolation_streak: number; policy: OpenAITwoFAAdmissionConfig } | undefined
  return s && ['pending', 'passed', 'isolated', 'isolation_pending'].includes(s.phase) && s.policy && typeof s.pass_streak === 'number' && typeof s.isolation_streak === 'number' ? s : null
})
</script>
