<template>
  <div class="border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="account-auto-bps">
    <div class="flex items-center justify-between gap-4">
      <div>
        <label class="input-label mb-0">{{ t('admin.accounts.openai.autoBPS') }}</label>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.openai.autoBPSDesc') }}</p>
      </div>
      <button type="button" role="switch" :aria-checked="draft.enabled" :disabled="locked"
        :aria-label="t('admin.accounts.openai.autoBPS')" data-testid="account-auto-bps-toggle"
        @click="draft.enabled = !draft.enabled"
        :class="['relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50', draft.enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600']">
        <span :class="['pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow transition', draft.enabled ? 'translate-x-5' : 'translate-x-0']" />
      </button>
    </div>
    <p v-if="loading" class="mt-2 text-xs text-gray-500" data-testid="account-auto-bps-loading">{{ t('admin.accounts.openai.autoBPSLoading') }}</p>
    <p v-else-if="loadError" class="mt-2 text-xs text-red-600 dark:text-red-400" data-testid="account-auto-bps-load-error">{{ t('admin.accounts.openai.autoBPSLoadFailed', { error: loadError }) }}</p>
    <p v-else-if="conflictingRuleId != null" class="mt-2 text-xs text-amber-700 dark:text-amber-400" data-testid="account-auto-bps-conflict">
      {{ t('admin.accounts.openai.autoBPSRuleConflict', { id: conflictingRuleId }) }}
      <a href="/admin/account-quality" class="underline">{{ t('admin.accounts.openai.autoBPSManageRules') }}</a>
    </p>
    <p v-else-if="hasRule" class="mt-2 text-xs text-gray-500 dark:text-gray-400" data-testid="account-auto-bps-pause-hint">{{ t('admin.accounts.openai.autoBPSPauseHint') }}</p>
    <QualityBPSSettings v-if="draft.enabled && !locked" v-model:bps="draft.bps" v-model:auto-restore="draft.autoRestore" class="mt-3" :target-groups="targetGroups" />
  </div>
</template>

<script setup lang="ts">
// 添加/编辑 OpenAI OAuth 账号时的「降智后自动开启 BPS」开关，设置项与质量运维规则共用 QualityBPSSettings。
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import QualityBPSSettings from '@/components/admin/operations/QualityBPSSettings.vue'
import { useAuthStore } from '@/stores/auth'
import type { AdminGroup } from '@/types'
import type { AutoBPSDraft } from '@/utils/accountAutoBPS'

const props = withDefaults(defineProps<{ groups: AdminGroup[]; loading?: boolean; loadError?: string; hasRule?: boolean; conflictingRuleId?: number | null }>(), {
  loading: false, loadError: '', hasRule: false, conflictingRuleId: null
})
const draft = defineModel<AutoBPSDraft>('draft', { required: true })
const { t } = useI18n()
const authStore = useAuthStore()
// 规则没读出来前不让改，免得保存时拿不准该建还是该改。
const locked = computed(() => props.loading || !!props.loadError || props.conflictingRuleId != null)
const targetGroups = computed(() => props.groups.filter(group => group.platform === 'openai' || (!authStore.isSimpleMode && group.platform === 'composite')))
</script>
