<template>
                <div class="flex items-center justify-between gap-4">
                  <div class="min-w-0">
                    <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.gatewayForwarding.requestTimezoneEnabled') }}</h3>
                    <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.gatewayForwarding.requestTimezoneEnabledDesc') }}</p>
                  </div>
                  <Toggle id="request-timezone-enabled" :model-value="modelValue" @update:model-value="$emit('update:modelValue', $event)" />
                </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
defineProps<{ modelValue: boolean }>()
defineEmits<{ (e: 'update:modelValue', value: boolean): void }>()
const { t } = useI18n()
</script>
