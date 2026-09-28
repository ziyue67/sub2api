<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'

withDefaults(defineProps<{ strict?: boolean; showTitle?: boolean }>(), {
  strict: false,
  showTitle: true
})

const enabled = defineModel<boolean>('enabled', { required: true })
const baseRpm = defineModel<number | null>('baseRpm', { required: true })
const strategy = defineModel<'tiered' | 'sticky_exempt'>('strategy', { default: 'tiered' })
const stickyBuffer = defineModel<number | null>('stickyBuffer', { default: null })
const { t } = useI18n()
const strategies = [
  { value: 'tiered', label: 'strategyTiered', hint: 'strategyTieredHint' },
  { value: 'sticky_exempt', label: 'strategyStickyExempt', hint: 'strategyStickyExemptHint' }
] as const
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between gap-4">
      <div>
        <div v-if="showTitle" class="input-label mb-0">{{ t('admin.accounts.quotaControl.rpmLimit.label') }}</div>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t(strict ? 'admin.accounts.quotaControl.rpmLimit.openaiHint' : 'admin.accounts.quotaControl.rpmLimit.hint') }}
        </p>
      </div>
      <Toggle v-model="enabled" :aria-label="t('admin.accounts.quotaControl.rpmLimit.label')" />
    </div>

    <template v-if="enabled">
      <div>
        <label class="input-label">
          {{ t('admin.accounts.quotaControl.rpmLimit.baseRpm') }}
          <input
            v-model.number="baseRpm"
            type="number"
            min="1"
            max="1000"
            step="1"
            class="input mt-1"
            :placeholder="t('admin.accounts.quotaControl.rpmLimit.baseRpmPlaceholder')"
          />
        </label>
        <p class="input-hint">{{ t('admin.accounts.quotaControl.rpmLimit.baseRpmHint') }}</p>
      </div>

      <template v-if="!strict">
        <div>
          <div class="input-label">{{ t('admin.accounts.quotaControl.rpmLimit.strategy') }}</div>
          <div class="flex gap-2">
            <button
              v-for="option in strategies"
              :key="option.value"
              type="button"
              :aria-pressed="strategy === option.value"
              :class="[
                'flex-1 rounded-lg px-3 py-2 text-sm font-medium transition-all',
                strategy === option.value
                  ? 'bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-400'
                  : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-400 dark:hover:bg-dark-500'
              ]"
              @click="strategy = option.value"
            >
              <div>{{ t(`admin.accounts.quotaControl.rpmLimit.${option.label}`) }}</div>
              <div class="mt-0.5 text-[10px] opacity-70">{{ t(`admin.accounts.quotaControl.rpmLimit.${option.hint}`) }}</div>
            </button>
          </div>
        </div>
        <div v-if="strategy === 'tiered'">
          <label class="input-label">
            {{ t('admin.accounts.quotaControl.rpmLimit.stickyBuffer') }}
            <input
              v-model.number="stickyBuffer"
              type="number"
              min="1"
              step="1"
              class="input mt-1"
              :placeholder="t('admin.accounts.quotaControl.rpmLimit.stickyBufferPlaceholder')"
            />
          </label>
          <p class="input-hint">{{ t('admin.accounts.quotaControl.rpmLimit.stickyBufferHint') }}</p>
        </div>
      </template>
    </template>
  </div>
</template>
