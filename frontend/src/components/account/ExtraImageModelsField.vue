<template>
  <div class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="extra-image-models">
    <p class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ t('admin.accounts.openai.extraImageModelsTitle') }}</p>
    <label class="block">
      <span class="input-label">{{ t('admin.accounts.openai.extraImageModelsExact') }}</span>
      <textarea
        :value="models"
        rows="3"
        class="input w-full font-mono"
        placeholder="nai-diffusion-4-5-full"
        data-testid="extra-image-model-ids"
        @input="emit('update:models', ($event.target as HTMLTextAreaElement).value)"
      />
    </label>
    <label class="block">
      <span class="input-label">{{ t('admin.accounts.openai.extraImageModelsPrefixes') }}</span>
      <textarea
        :value="prefixes"
        rows="2"
        class="input w-full font-mono"
        placeholder="nai-diffusion-"
        data-testid="extra-image-model-prefixes"
        @input="emit('update:prefixes', ($event.target as HTMLTextAreaElement).value)"
      />
    </label>
    <p class="input-hint">{{ t('admin.accounts.openai.extraImageModelsHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

defineProps<{ models: string; prefixes: string }>()
const emit = defineEmits<{
  (event: 'update:models', value: string): void
  (event: 'update:prefixes', value: string): void
}>()
const { t } = useI18n()
</script>
