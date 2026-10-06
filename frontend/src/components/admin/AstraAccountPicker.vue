<template>
  <fieldset class="min-w-0 rounded-xl border border-gray-200 p-4 dark:border-dark-600">
    <legend class="px-1 text-sm font-medium text-gray-800 dark:text-gray-100">{{ label }} · {{ modelValue.length }}/64</legend>
    <input v-model="search" type="search" class="input mb-3 w-full" :aria-label="t('admin.astraGateway.search')" :placeholder="t('admin.astraGateway.search')" />
    <div class="max-h-52 space-y-1 overflow-y-auto">
      <label v-for="account in filtered" :key="account.id" class="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-2 text-sm hover:bg-gray-50 dark:hover:bg-dark-700">
        <input type="checkbox" :checked="modelValue.includes(account.id)" :disabled="disabled || (!modelValue.includes(account.id) && modelValue.length >= 64)" @change="select(account.id, ($event.target as HTMLInputElement).checked)" />
        <span class="min-w-0 break-words text-gray-700 dark:text-gray-200">#{{ account.id }} · {{ account.name }}</span>
      </label>
      <p v-if="!filtered.length" class="py-3 text-sm text-gray-500">{{ t('admin.astraGateway.noAccounts') }}</p>
    </div>
    <div v-if="missing.length" class="mt-3 space-y-2">
      <p class="text-xs text-amber-600">{{ t('admin.astraGateway.missingAccounts') }}</p>
      <button v-for="id in missing" :key="id" type="button" class="btn btn-secondary btn-sm mr-2" :disabled="disabled" @click="select(id, false)">#{{ id }} · {{ t('common.remove') }}</button>
    </div>
  </fieldset>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
const props = defineProps<{ label: string; modelValue: number[]; accounts: { id: number; name: string }[]; disabled?: boolean }>()
const emit = defineEmits<{ (event: 'update:modelValue', value: number[]): void }>()
const { t } = useI18n()
const search = ref('')
const filtered = computed(() => props.accounts.filter(a => `${a.id} ${a.name}`.toLowerCase().includes(search.value.trim().toLowerCase())))
const missing = computed(() => props.modelValue.filter(id => !props.accounts.some(a => a.id === id)))
function select(id: number, checked: boolean) {
  emit('update:modelValue', checked ? [...new Set([...props.modelValue, id])] : props.modelValue.filter(value => value !== id))
}
</script>
