<template>
  <div class="space-y-1.5" data-testid="test-model-select">
    <div class="flex items-center justify-between gap-2">
      <label :for="id" class="input-label">{{ label }}</label>
      <button type="button" class="text-xs text-primary-600 disabled:opacity-50 dark:text-primary-400"
        :disabled="disabled" data-testid="model-input-toggle" @click="manual = !manual">
        {{ t(manual ? 'admin.accounts.pelicanTest.chooseModel' : 'admin.accounts.pelicanTest.enterModel') }}
      </button>
    </div>
    <input v-if="manual" :id="id" class="input w-full" type="text" autocomplete="off"
      :value="modelValue" :disabled="disabled" :placeholder="t('admin.accounts.pelicanTest.modelPlaceholder')"
      data-testid="manual-model-input" @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)" />
    <Select v-else :id="id" :model-value="modelValue" :options="options" searchable creatable
      :aria-label="label" :disabled="disabled" @update:model-value="emit('update:modelValue', String($event ?? ''))" />
    <p class="input-hint">{{ hint }}</p>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'

defineProps<{
  id: string
  modelValue: string
  options: Array<{ value: string; label: string }>
  label: string
  hint: string
  disabled?: boolean
}>()
const emit = defineEmits<{ (event: 'update:modelValue', value: string): void }>()
const { t } = useI18n()
const manual = ref(false)
</script>
