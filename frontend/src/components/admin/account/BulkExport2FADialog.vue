<template>
  <BaseDialog :show="opened" :title="t('admin.accounts.bulkExport2fa.title')" width="wide" @close="close">
    <div class="space-y-4">
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.bulkExport2fa.hint', { count: accountIds.length }) }}</p>
      <label class="flex items-center gap-2 text-sm">
        <input v-model="includePassword" type="checkbox" :disabled="busy" @change="clearResult" />
        {{ t('tokenGuardV2.totpManage.includePassword') }}
      </label>
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t(includePassword ? 'admin.accounts.bulkExport2fa.comboFormat' : 'admin.accounts.bulkExport2fa.secretFormat') }}</p>
      <button type="button" class="btn btn-primary" data-testid="generate" :disabled="busy || !accountIds.length" @click="generate">
        {{ t('admin.accounts.bulkExport2fa.generate') }}
      </button>
      <p v-if="busy" role="status" class="text-sm">{{ t('admin.accounts.bulkExport2fa.progress', { done: completed, total: accountIds.length }) }}</p>
      <template v-if="finished">
        <p role="status" class="text-sm">{{ t('admin.accounts.bulkExport2fa.summary', { success: successCount, failed: failures.length }) }}</p>
        <label v-if="text" class="block text-sm">
          {{ t('admin.accounts.bulkExport2fa.backupText') }}
          <textarea :value="text" readonly rows="10" spellcheck="false" autocomplete="off" class="input mt-2 w-full whitespace-pre font-mono" data-testid="backup-text" @focus="selectText" />
        </label>
        <div v-if="failures.length" class="max-h-40 overflow-auto text-sm text-amber-700 dark:text-amber-300" role="alert">
          <p>{{ t('admin.accounts.bulkExport2fa.failedHint') }}</p>
          <ul class="mt-1 list-inside list-disc">
            <li v-for="failure in failures" :key="failure.id">{{ t('admin.accounts.bulkExport2fa.failedAccount', { id: failure.id }) }}：{{ t(failure.reason) }}</li>
          </ul>
        </div>
      </template>
    </div>
    <template #footer>
      <button type="button" class="btn btn-secondary" @click="close">{{ t('common.close') }}</button>
      <button type="button" class="btn btn-secondary" data-testid="copy" :disabled="!text || busy" @click="copyToClipboard(text)">{{ t('common.copy') }}</button>
      <button type="button" class="btn btn-primary" data-testid="download" :disabled="!text || busy" @click="download">{{ t('admin.accounts.bulkExport2fa.download') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { exportTOTP } from '@/api/admin/openaiTotp'
import { useClipboard } from '@/composables/useClipboard'

const props = defineProps<{ accountIds: number[] }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const opened = ref(true)
const includePassword = ref(false)
const busy = ref(false)
const finished = ref(false)
const completed = ref(0)
const successCount = ref(0)
const text = ref('')
const failures = ref<{ id: number; reason: string }[]>([])
let controller: AbortController | undefined
let generation = 0

function clearResult() {
  text.value = ''
  failures.value = []
  completed.value = 0
  successCount.value = 0
  finished.value = false
}

function dispose() {
  generation++
  controller?.abort()
  controller = undefined
  opened.value = false
  busy.value = false
  clearResult()
}

function close() { dispose(); emit('close') }
onBeforeUnmount(dispose)

// Only safe, local messages reach the UI; upstream error bodies may contain credentials.
function failureReason(error: unknown): string {
  const { reason, code } = (error ?? {}) as { reason?: string; code?: string }
  const marker = reason || code
  if (marker === 'TOTP_UNRESOLVED') return 'admin.accounts.bulkExport2fa.unresolved'
  if (marker === 'TOTP_CONFIG_REQUIRED') return 'admin.accounts.bulkExport2fa.notConfigured'
  if (marker === 'TOTP_IDENTITY_MISMATCH') return 'admin.accounts.bulkExport2fa.identityMismatch'
  return 'admin.accounts.bulkExport2fa.failed'
}

async function generate() {
  if (busy.value || !opened.value) return
  clearResult()
  const current = ++generation
  const ids = [...new Set(props.accountIds)]
  const withPassword = includePassword.value
  const requestController = new AbortController()
  controller = requestController
  busy.value = true
  // Bounded concurrency, with deterministic ordering independent of response timing.
  const lines = new Array<string>(ids.length)
  const errors = new Array<{ id: number; reason: string }>(ids.length)
  let next = 0
  async function worker() {
    while (current === generation && next < ids.length) {
      const index = next++
      const id = ids[index]!
      try {
        const data = await exportTOTP(id, 'current', withPassword, requestController.signal)
        if (current !== generation) return
        const fields = withPassword ? [data.email, data.password, data.secret] : [data.email, data.secret]
        if (fields.some(field => !field || /[\r\n]|----/.test(field))) {
          errors[index] = { id, reason: 'admin.accounts.bulkExport2fa.invalidFields' }
        } else {
          lines[index] = fields.join('----')
        }
      } catch (error) {
        if (current !== generation) return
        errors[index] = { id, reason: failureReason(error) }
      }
      completed.value++
    }
  }
  await Promise.all(Array.from({ length: Math.min(3, ids.length) }, worker))
  if (current !== generation) return
  const exported = lines.filter(Boolean)
  text.value = exported.length ? exported.join('\n') + '\n' : ''
  successCount.value = exported.length
  failures.value = errors.filter(Boolean)
  finished.value = true
  busy.value = false
  controller = undefined
}

function selectText(event: FocusEvent) { (event.target as HTMLTextAreaElement).select() }

function download() {
  if (!text.value || busy.value) return
  const url = URL.createObjectURL(new Blob([text.value], { type: 'text/plain;charset=utf-8' }))
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = `openai-2fa-backup-${new Date().toISOString().replace(/[:.]/g, '-')}.txt`
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
</script>
