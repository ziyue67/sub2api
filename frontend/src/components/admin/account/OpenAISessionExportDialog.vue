<template>
  <BaseDialog :show="!!account" :title="t('admin.accounts.sessionExport.title')" width="normal" @close="close">
    <form class="space-y-4" @submit.prevent="exportSession">
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.sessionExport.hint') }}</p>
      <p class="text-sm font-medium">{{ account?.name }}</p>
      <label class="block text-sm">
        {{ t('admin.accounts.sessionExport.cookie') }}
        <input v-model="cookie" type="password" autocomplete="off" class="input mt-2 w-full font-mono" :disabled="busy" :placeholder="t('admin.accounts.sessionExport.placeholder')" />
      </label>
      <p class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.accounts.sessionExport.sensitive') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" @click="close">{{ t('common.cancel') }}</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || !cookie.trim()">{{ t(busy ? 'admin.accounts.sessionExport.exporting' : 'admin.accounts.sessionExport.download') }}</button>
      </div>
    </form>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp, StepUpCancelledError, isStepUpCancelled, isStepUpBlocked, stepUpBlockReason } from '@/composables/useStepUp'
import { exportOpenAISession } from '@/api/admin/accounts'
import type { Account } from '@/types'

const props = defineProps<{ account: Account | null }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const cookie = ref(''), error = ref(''), busy = ref(false)
const stepUp = useStepUp()
let generation = 0
watch(() => props.account, clear)
function clear() { generation++; cookie.value = ''; error.value = ''; busy.value = false; stepUp.onCancel() }
function close() { clear(); emit('close') }
onBeforeUnmount(clear)
async function exportSession() {
  if (!props.account || busy.value || !cookie.value.trim()) return
  const id = props.account.id, current = generation, loginCookie = cookie.value.trim()
  busy.value = true; error.value = ''
  try {
    const session = await stepUp.run(async () => {
      if (current !== generation) throw new StepUpCancelledError()
      try {
        return await exportOpenAISession(id, loginCookie)
      } catch (err) {
        // Ignore stale errors before useStepUp can open a verification prompt.
        if (current !== generation) throw new StepUpCancelledError()
        throw err
      }
    })
    if (current !== generation) return
    const url = URL.createObjectURL(new Blob([JSON.stringify(session, null, 2)], { type: 'application/json' }))
    const link = document.createElement('a')
    link.href = url; link.download = `chatgpt-session-${id}.json`
    document.body.appendChild(link); link.click(); link.remove()
    setTimeout(() => URL.revokeObjectURL(url), 0)
    close()
  } catch (err) {
    if (current !== generation || isStepUpCancelled(err)) return
    const reason = (err as { reason?: string })?.reason
    const errorKey = reason?.startsWith('OPENAI_SESSION_') ? reason : 'failed'
    error.value = isStepUpBlocked(err)
      ? t(stepUpBlockReason(err) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? 'stepUp.adminApiKeyForbidden' : 'stepUp.notEnabled')
      : t('admin.accounts.sessionExport.' + errorKey)
  } finally {
    if (current === generation) { busy.value = false; cookie.value = '' }
  }
}
</script>
