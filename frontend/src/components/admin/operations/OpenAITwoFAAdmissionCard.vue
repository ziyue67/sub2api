<template>
  <section class="space-y-5 rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-900" data-testid="two-fa-admission">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="text-lg font-semibold">{{ t('autoConfig.admission.title') }}</h2>
      <label class="flex items-center gap-2 text-sm"><input v-model="model.enabled" data-testid="admission-enabled" type="checkbox" role="switch" class="h-4 w-4 rounded text-primary-600" />{{ t('autoConfig.enable') }}</label>
    </header>
    <p class="text-sm leading-6 text-gray-500">{{ t('autoConfig.admission.hint') }}</p>
    <div class="grid min-w-0 gap-4 sm:grid-cols-2 xl:grid-cols-3">
      <label class="min-w-0"><span class="mb-2 block text-sm font-medium">{{ t('autoConfig.admission.model') }}</span><input v-model="model.model_id" data-testid="admission-model" class="input w-full" maxlength="100" required /></label>
      <label><span class="mb-2 block text-sm font-medium">{{ t('autoConfig.admission.effort') }}</span><select v-model="model.reasoning_effort" data-testid="admission-effort" class="input w-full"><option v-for="effort in efforts" :key="effort" :value="effort">{{ t('autoConfig.admission.efforts.' + effort) }} ({{ effort }})</option></select></label>
      <label v-for="field in fields" :key="field.key"><span class="mb-2 block text-sm font-medium">{{ t('autoConfig.admission.' + field.key) }}</span><input v-model.number="model[field.key]" :data-testid="'admission-' + field.key" type="number" min="1" :max="field.max" step="1" required class="input w-full" /></label>
    </div>
    <p class="rounded-xl bg-emerald-50 p-3 text-sm leading-6 text-emerald-800 dark:bg-emerald-950/30 dark:text-emerald-200">{{ t('autoConfig.admission.passHint', { count: model.pass_count, concurrency: model.passed_concurrency }) }}</p>
    <label class="block"><span class="mb-2 block text-sm font-medium">{{ t('autoConfig.admission.group') }}</span><select v-model.number="model.isolation_group_id" data-testid="admission-group" class="input w-full" :required="model.enabled">
      <option :value="0">{{ t('autoConfig.admission.chooseGroup') }}</option>
      <option v-if="model.isolation_group_id && !availableGroups.some(g => g.id === model.isolation_group_id)" :value="model.isolation_group_id" disabled>{{ t('autoConfig.admission.unavailableGroup', { id: model.isolation_group_id }) }}</option>
      <option v-for="group in availableGroups" :key="group.id" :value="group.id">{{ group.name }} · #{{ group.id }} · {{ group.platform }} · {{ t('autoConfig.admission.active') }}</option>
    </select></label>
    <p class="text-sm leading-6 text-gray-500">{{ t('autoConfig.admission.isolationRule', { count: model.isolation_count }) }}</p>
    <p class="rounded-xl bg-amber-50 p-3 text-sm leading-6 text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ t('autoConfig.admission.isolationHint') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Group } from '@/types'
import type { OpenAITwoFAAdmissionConfig } from '@/utils/openaiTwoFAAdmission'
const model = defineModel<OpenAITwoFAAdmissionConfig>({ required: true })
const props = defineProps<{ groups: Group[] }>()
const { t } = useI18n()
const availableGroups = computed(() => props.groups.filter(g => g.platform === 'openai' && g.status === 'active'))
const efforts = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra']
const fields = [{ key: 'wait_seconds', max: 86400 }, { key: 'pass_count', max: 1000 }, { key: 'passed_concurrency', max: 10000 }, { key: 'isolation_count', max: 1000 }] as const
</script>
