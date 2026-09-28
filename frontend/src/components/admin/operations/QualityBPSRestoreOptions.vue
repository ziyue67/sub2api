<template>
  <div>
    <label class="flex items-center gap-2 font-medium"><input v-model="autoRestore" type="checkbox" data-testid="quality-bps-auto-disable" />{{ t('qualityOps.bpsAutoDisable') }}</label>
    <p class="mt-1 pl-6 text-xs text-gray-500">{{ t('qualityOps.bpsRestoreHelp') }}</p>
    <div v-if="autoRestore" class="mt-3 space-y-3 pl-6" data-testid="quality-bps-restore-options">
      <label class="block space-y-1"><span>{{ t('qualityOps.bpsPassThreshold') }}</span><input v-model.number="bps.pass_threshold" type="number" min="1" max="100" step="1" class="input sm:w-40" data-testid="quality-bps-pass-threshold" /><span class="block text-xs text-gray-500">{{ t('qualityOps.bpsPassThresholdHint') }}</span></label>
      <div v-if="holdShown">
        <label class="flex items-center gap-2"><input v-model="bps.hold_on_usage" type="checkbox" data-testid="quality-bps-hold-on-usage" />{{ t('qualityOps.bpsHoldOnUsage') }}</label>
        <p class="mt-1 pl-6 text-xs text-gray-500">{{ t('qualityOps.bpsHoldOnUsageHint') }}</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
// 「满血后自动关闭 BPS」及其关闭条件：连续满血几次才关、用量仍高时是否先不关。
// BPS 设置里和批量只改「自动恢复」时共用。
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { QualityBPSPolicy } from '@/types'

// alwaysShowHold：批量修改时各规则的开启条件不同，始终给出「用量仍高时先不关」。
const props = withDefaults(defineProps<{ alwaysShowHold?: boolean }>(), { alwaysShowHold: false })
const bps = defineModel<QualityBPSPolicy>('bps', { required: true })
const autoRestore = defineModel<boolean>('autoRestore', { default: false })
const { t } = useI18n()
// 只有按用量开启、且不是「同时满足」时，用量才会单独让 BPS 保持开启（与后端 QualityBPSHoldForUsage 一致）。
const holdShown = computed(() => props.alwaysShowHold || (bps.value.usage_percent > 0 && !(bps.value.require_all && bps.value.failure_threshold > 0)))
</script>
