<template>
  <div class="border-t pt-4" data-testid="minimax-h3-video-pricing">
    <label class="flex items-center gap-2 font-medium text-gray-700 dark:text-gray-300">
      <input type="checkbox" class="rounded" :checked="enabled" @change="toggle" />
      {{ t('admin.groups.videoPricing.h3Enable') }}
    </label>
    <div v-if="enabled" class="mt-3 space-y-3">
      <label class="block">
        <span class="input-label">{{ t('admin.groups.videoPricing.h3Mode') }}</span>
        <select class="input" :value="mode" @change="changeMode">
          <option value="per_second">{{ t('admin.groups.videoPricing.h3PerSecond') }}</option>
          <option value="per_request">{{ t('admin.groups.videoPricing.h3PerRequest') }}</option>
        </select>
      </label>
      <label v-if="mode === 'per_request'" class="block">
        <span class="input-label">{{ t('admin.groups.videoPricing.h3Seconds') }}</span>
        <input type="number" class="input" min="1" max="15" step="1" required :value="seconds" @input="changeSeconds" />
      </label>
      <label class="block">
        <span class="input-label">{{ t(mode === 'per_second' ? 'admin.groups.videoPricing.h3SecondPrice' : 'admin.groups.videoPricing.h3RequestPrice') }}</span>
        <input type="number" class="input" min="0" step="any" required :value="price ?? ''" @input="changePrice" />
      </label>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.groups.videoPricing.h3Hint') }}</p>
      <p v-if="mode === 'per_request'" class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.groups.videoPricing.h3DurationHint') }}</p>
      <p class="rounded-lg bg-gray-50 p-3 text-xs dark:bg-gray-800">
        {{ t('admin.groups.videoPricing.h3Preview', { multiplier, price: finalPrice }) }}
        {{ t(mode === 'per_second' ? 'admin.groups.videoPricing.h3PerSecond' : 'admin.groups.videoPricing.h3PerRequest') }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { VideoModelPricesForm } from './groupsVideoModelPricing'

const props = defineProps<{ modelValue: VideoModelPricesForm; multiplier: number }>()
const emit = defineEmits<{ 'update:modelValue': [value: VideoModelPricesForm] }>()
const { t } = useI18n()
const family = 'minimax-h3'
const tariff = computed(() => props.modelValue[family] ?? {})
const tariffKey = computed(() => Object.keys(tariff.value).find(key => key === 'per_second' || /^request_\d+s$/.test(key)))
const enabled = computed(() => tariffKey.value !== undefined)
const mode = computed(() => tariffKey.value?.startsWith('request_') ? 'per_request' : 'per_second')
const seconds = computed(() => Number(tariffKey.value?.match(/^request_(\d+)s$/)?.[1] ?? 1))
const price = computed(() => tariffKey.value ? tariff.value[tariffKey.value] : null)
const finalPrice = computed(() => price.value === null || price.value === '' ? t('admin.groups.videoPricing.notConfigured') : `$${(Number(price.value) * props.multiplier).toFixed(6)}`)

function update(key?: string, value: number | string | null = null) {
  const next = { ...props.modelValue }
  delete next[family]
  if (key) next[family] = { [key]: value }
  emit('update:modelValue', next)
}
function toggle(event: Event) {
  update((event.target as HTMLInputElement).checked ? 'per_second' : undefined)
}
function changeMode(event: Event) {
  update((event.target as HTMLSelectElement).value === 'per_request' ? `request_${seconds.value}s` : 'per_second', price.value ?? null)
}
function changeSeconds(event: Event) {
  const value = Number((event.target as HTMLInputElement).value)
  if (Number.isInteger(value) && value >= 1 && value <= 15) update(`request_${value}s`, price.value ?? null)
}
function changePrice(event: Event) {
  const raw = (event.target as HTMLInputElement).value
  // Keep intermediate decimal input such as "0." intact until serialization.
  update(tariffKey.value, raw === '' ? null : raw)
}
</script>
