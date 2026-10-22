<template>
  <div :data-key-url="row.key_hash" class="mt-2 space-y-2 border-t border-gray-100 pt-2 dark:border-dark-700">
    <p class="break-all text-xs text-gray-600 dark:text-gray-300">{{ t('admin.riskControl.keyUrl') }}: {{ row.base_url }}</p>
    <template v-if="editing">
      <label class="block text-xs">
        {{ t('admin.riskControl.keyUrl') }}
        <input v-model.trim="draft" type="url" class="input mt-1" :disabled="busy" @keydown.esc="cancel" />
      </label>
      <p class="text-xs text-gray-500">{{ t('admin.riskControl.keyUrlHint') }}</p>
      <div class="flex gap-2">
        <button data-test="key-url-save" type="button" class="btn btn-primary btn-sm" :disabled="busy || disabled" @click="save">{{ t(busy ? 'common.saving' : 'common.save') }}</button>
        <button data-test="key-url-cancel" type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="cancel">{{ t('common.cancel') }}</button>
      </div>
    </template>
    <button v-else data-test="key-url-edit" type="button" class="text-xs font-medium text-primary-600 dark:text-primary-400" :disabled="disabled" @click="start">{{ t('admin.riskControl.editKeyUrl') }}</button>
    <p v-if="error" role="alert" class="text-xs text-red-600">{{ error }}</p>
    <p v-if="saved" role="status" class="text-xs text-emerald-600">{{ t('admin.riskControl.keyUrlSaved') }}</p>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ContentModerationAPIKeyStatus } from '@/api/admin/riskControl'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{
  row: ContentModerationAPIKeyStatus
  disabled?: boolean
  persist: (hash: string, baseURL: string, expected: string) => Promise<void>
}>()
const { t } = useI18n()
const editing = ref(false)
const busy = ref(false)
const draft = ref('')
const expected = ref('')
const error = ref('')
const saved = ref(false)
function start() {
  draft.value = props.row.base_url ?? ''
  expected.value = draft.value
  error.value = ''
  saved.value = false
  editing.value = true
}
function cancel() {
  if (busy.value) return
  editing.value = false
  error.value = ''
}
async function save() {
  if (busy.value || props.disabled) return
  error.value = ''
  try {
    const url = new URL(draft.value)
    if (!['https:', 'http:'].includes(url.protocol) || !url.hostname || url.username || url.password || url.search || url.hash || draft.value.includes('?') || draft.value.includes('#')) throw new Error('invalid URL')
  } catch {
    error.value = t('admin.riskControl.keyUrlInvalid')
    return
  }
  busy.value = true
  try {
    await props.persist(props.row.key_hash, draft.value, expected.value)
    editing.value = false
    saved.value = true
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('admin.riskControl.keyUrlSaveFailed'))
  } finally {
    busy.value = false
  }
}
</script>
