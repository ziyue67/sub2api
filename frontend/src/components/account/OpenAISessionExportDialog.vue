<template>
  <BaseDialog :show="!!account" :title="t('tokenGuardV2.totpManage.sessionExport.title')" width="normal" @close="close">
    <div class="space-y-4">
      <p class="text-sm font-medium">{{ account?.name }}</p>
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('tokenGuardV2.totpManage.sessionExport.hint') }}</p>
      <div v-if="task" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-700" role="status">
        <p class="text-sm font-medium">{{ stageLabel(task.stage) }}</p>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ task.status }}</p>
      </div>
      <p v-else-if="busy" class="text-sm" role="status">{{ t('tokenGuardV2.totpManage.sessionExport.starting') }}</p>
      <p v-if="error" class="text-sm text-red-600" role="alert">{{ error }}</p>
      <div class="flex justify-end gap-2">
        <button type="button" data-testid="close" class="btn btn-secondary" @click="close">{{ t('common.cancel') }}</button>
        <button type="button" data-testid="export" class="btn btn-primary" :disabled="busy || !account" @click="start">{{ t(busy ? 'tokenGuardV2.totpManage.sessionExport.exporting' : 'tokenGuardV2.totpManage.sessionExport.download') }}</button>
      </div>
    </div>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp, StepUpCancelledError, isStepUpCancelled, isStepUpBlocked, stepUpBlockReason } from '@/composables/useStepUp'
import { exportChatGPTSession, getChatGPTSessionExportTask, downloadChatGPTSession } from '@/api/admin/openaiTotp'
import type { OpenAISessionExportTask } from '@/api/admin/openaiTotp'

const props = defineProps<{ account: { id: number; name: string } | null }>()
const emit = defineEmits<{ close: [] }>()
const { t, te } = useI18n()
const task = ref<OpenAISessionExportTask | null>(null)
const error = ref('')
const busy = ref(false)
const stepUp = useStepUp()
let generation = 0
let pollTimer: ReturnType<typeof setTimeout> | null = null

const stageKeys: Record<string, string> = {
  queued: 'queued', starting: 'starting', protocol_connecting: 'connecting',
  password_submitted: 'passwordSubmitted', mfa_submitted: 'totpSubmitted',
  session_request: 'sessionRequest', identity_verification: 'identityVerification',
  succeeded: 'succeeded', failed: 'failed'
}

function stageLabel(stage: string) {
  const key = stageKeys[stage] || 'starting'
  const path = `tokenGuardV2.totpManage.sessionExport.stages.${key}`
  return te(path) ? t(path) : stage
}

function clear() {
  generation++
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = null
  task.value = null
  error.value = ''
  busy.value = false
  stepUp.onCancel()
}

function close() { clear(); emit('close') }
onBeforeUnmount(clear)

function showError(err: unknown) {
  if (isStepUpCancelled(err)) return
  if (isStepUpBlocked(err)) {
    error.value = t(stepUpBlockReason(err) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? 'stepUp.adminApiKeyForbidden' : 'stepUp.notEnabled')
    return
  }
  const code = String((err as { reason?: string; code?: string })?.reason || (err as { code?: string })?.code || '')
  const path = `tokenGuardV2.totpManage.sessionExport.errors.${code}`
    error.value = te(path) ? t(path) : t('tokenGuardV2.totpManage.sessionExport.failed')
}

function runCurrent<T>(current: number, action: () => Promise<T>) {
  return stepUp.run(async () => {
    if (current !== generation) throw new StepUpCancelledError()
    return action()
  })
}

async function poll(id: number, taskId: number, current: number, delay = 1000, elapsed = 0): Promise<void> {
  if (current !== generation) return
  if (elapsed >= 5 * 60 * 1000) throw Object.assign(new Error('expired'), { reason: 'expired' })
  await new Promise(resolve => { pollTimer = setTimeout(resolve, delay) })
  if (current !== generation) return
  const next = await getChatGPTSessionExportTask(id, taskId)
  if (current !== generation) return
  task.value = next
  if (next.status === 'succeeded') {
    const session = await runCurrent(current, () => downloadChatGPTSession(id, taskId))
    if (current !== generation) return
    const compactSession = {
      accessToken: typeof session.accessToken === 'string' ? session.accessToken : '',
      sessionToken: typeof session.sessionToken === 'string' ? session.sessionToken : ''
    }
    if (!compactSession.accessToken || !compactSession.sessionToken) {
      throw Object.assign(new Error('session_token_missing'), { reason: 'session_token_missing' })
    }
    const url = URL.createObjectURL(new Blob([JSON.stringify(compactSession, null, 2)], { type: 'application/json' }))
    const link = document.createElement('a')
    link.href = url; link.download = `chatgpt-session-${id}.json`; document.body.appendChild(link); link.click(); link.remove()
    setTimeout(() => URL.revokeObjectURL(url), 0)
    close()
    return
  }
  if (next.status === 'failed' || next.status === 'expired') throw Object.assign(new Error(next.error_code || next.status), { reason: next.error_code || next.status })
  return poll(id, taskId, current, Math.min(delay * 1.5, 5000), elapsed + delay)
}

async function start() {
  if (!props.account || busy.value) return
  const id = props.account.id, current = generation
  busy.value = true; error.value = ''; task.value = null
  try {
    const created = await runCurrent(current, () => exportChatGPTSession(id))
    if (current !== generation) return
    task.value = created
    await poll(id, created.id, current)
  } catch (err) {
    if (current === generation) showError(err)
  } finally {
    if (current === generation) busy.value = false
  }
}

watch(() => props.account, (account) => {
  clear()
  if (account) void start()
}, { immediate: true })
</script>
