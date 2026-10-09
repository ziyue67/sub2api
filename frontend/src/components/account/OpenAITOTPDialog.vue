<template>
  <button type="button" class="link-btn" data-testid="totp-manage" :disabled="!configured" @click="open">{{ t('tokenGuardV2.totpManage.title') }}</button>
  <BaseDialog :show="opened" :title="t('tokenGuardV2.totpManage.title') + ' · ' + accountName" width="normal" @close="close">
    <div class="space-y-4">
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('tokenGuardV2.totpManage.hint') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="task" data-testid="totp-status" class="text-sm font-medium">{{ t('tokenGuardV2.totpManage.status') }}: {{ t('tokenGuardV2.totpManage.states.' + task.state) }}</p>
      <p v-if="task?.state === 'uncertain'" class="text-sm text-amber-700 dark:text-amber-300">{{ t('tokenGuardV2.totpManage.uncertainHint') }}</p>
      <p v-if="task?.state === 'succeeded'" class="text-sm text-green-700 dark:text-green-300">{{ t(task.action === 'verify_old' ? 'tokenGuardV2.totpManage.keptOld' : 'tokenGuardV2.totpManage.completed') }}</p>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn btn-primary" data-testid="totp-rotate" :disabled="busy || !loaded || active" @click="start">{{ t('tokenGuardV2.totpManage.rotate') }}</button>
        <template v-if="task?.state === 'uncertain'">
          <button type="button" class="btn btn-secondary" data-testid="totp-verify-new" :disabled="busy || !task.has_candidate" @click="verify('verify_new')">{{ t('tokenGuardV2.totpManage.verifyNew') }}</button>
          <button type="button" class="btn btn-secondary" :disabled="busy" @click="verify('verify_old')">{{ t('tokenGuardV2.totpManage.verifyOld') }}</button>
        </template>
      </div>
      <hr class="dark:border-gray-700" />
      <label class="block text-sm">{{ t('tokenGuardV2.totpManage.exportSource') }}
        <select v-model="source" class="input mt-1 w-full" :disabled="busy" @change="clearExport">
          <option value="current" :disabled="active">{{ t('tokenGuardV2.totpManage.current') }}</option>
          <option v-if="task?.state === 'uncertain' && task.has_candidate" value="candidate">{{ t('tokenGuardV2.totpManage.candidate') }}</option>
          <option v-if="task?.state === 'uncertain'" value="previous">{{ t('tokenGuardV2.totpManage.previous') }}</option>
        </select>
      </label>
      <label class="flex items-center gap-2 text-sm"><input v-model="includePassword" type="checkbox" @change="clearExport" />{{ t('tokenGuardV2.totpManage.includePassword') }}</label>
      <button type="button" class="btn btn-secondary" data-testid="totp-export" :disabled="busy || !loaded || (active && task?.state !== 'uncertain')" @click="reveal">{{ t('tokenGuardV2.totpManage.export') }}</button>
      <div v-if="exported" class="space-y-3" data-testid="totp-export-result">
        <p v-if="exported.state === 'uncertain'" class="text-sm text-amber-700">{{ t('tokenGuardV2.totpManage.recoveryLabel') }}</p>
        <img v-if="qr" :src="qr" :alt="t('tokenGuardV2.totpManage.qr')" class="h-48 w-48 rounded bg-white" />
        <label class="block text-sm">{{ t('tokenGuardV2.totpManage.secret') }}<input :value="exported.secret" readonly class="input mt-1 w-full font-mono" autocomplete="off" /></label>
        <div class="flex flex-wrap gap-2">
          <button type="button" class="btn btn-secondary" @click="download('json')">{{ t('tokenGuardV2.totpManage.downloadJSON') }}</button>
          <button type="button" class="btn btn-secondary" @click="download('txt')">{{ t(includePassword ? 'tokenGuardV2.totpManage.downloadCombo' : 'tokenGuardV2.totpManage.downloadSecret') }}</button>
        </div>
      </div>
    </div>
    <template #footer><button type="button" class="btn btn-secondary" @click="close">{{ t('tokenGuardV2.totpManage.close') }}</button></template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import QRCode from 'qrcode'
import { BaseDialog } from '@/components/common'
import { exportTOTP, getTOTPRotation, rotateTOTP, verifyTOTP, type TOTPExport, type TOTPExportSource, type TOTPRotation } from '@/api/admin/openaiTotp'

const props = defineProps<{ accountId: number; accountName: string; configured: boolean }>()
const { t } = useI18n()
const opened = ref(false), busy = ref(false), loaded = ref(false)
const task = ref<TOTPRotation | null>(null)
const error = ref(''), qr = ref('')
const source = ref<TOTPExportSource>('current'), includePassword = ref(false)
const exported = ref<TOTPExport | null>(null)
const active = computed(() => !!task.value && !['succeeded', 'failed'].includes(task.value.state))
let timer: ReturnType<typeof setInterval> | undefined
let generation = 0
function clearExport() { exported.value = null; qr.value = '' }
function close() {
  generation++; opened.value = false; busy.value = false; loaded.value = false; includePassword.value = false
  clearExport(); error.value = ''; if (timer) clearInterval(timer); timer = undefined
}
async function refresh() {
  const current = generation
  try {
    const result = await getTOTPRotation(props.accountId)
    if (!opened.value || current !== generation) return
    task.value = result; loaded.value = true
    if (result?.state === 'uncertain' && source.value === 'current') source.value = result.has_candidate ? 'candidate' : 'previous'
    if (result?.state !== 'uncertain' && source.value !== 'current') { source.value = 'current'; clearExport() }
  } catch { if (opened.value && current === generation) error.value = t('tokenGuardV2.error') }
}
async function open() {
  opened.value = true; generation++; source.value = 'current'; task.value = null
  await refresh()
  if (opened.value) timer = setInterval(() => { if (!busy.value) void refresh() }, 3000)
}
async function start() {
  if (!window.confirm(t('tokenGuardV2.totpManage.confirm', { account: props.accountName }))) return
  const current = generation
  await perform(async () => { const result = await rotateTOTP(props.accountId); if (opened.value && current === generation) task.value = result })
}
async function verify(action: 'verify_new' | 'verify_old') {
  if (!task.value) return
  const id = task.value.task_id
  await perform(async () => { await verifyTOTP(props.accountId, id, action); await refresh() })
}
async function perform(work: () => Promise<void>) {
  const current = generation; busy.value = true; error.value = ''; clearExport()
  try { await work() } catch { if (opened.value && current === generation) error.value = t('tokenGuardV2.error') }
  finally { if (current === generation) busy.value = false }
}
async function reveal() {
  const current = generation
  await perform(async () => {
    const data = await exportTOTP(props.accountId, source.value, includePassword.value)
    if (!opened.value || current !== generation) return
    exported.value = data
    const image = await QRCode.toDataURL(data.otpauth_uri, { width: 192, margin: 2 })
    if (opened.value && current === generation) qr.value = image
  })
}
function download(format: 'json' | 'txt') {
  const data = exported.value; if (!data) return
  if (format === 'txt' && includePassword.value && /[\r\n]|----/.test(data.password || '')) {
    error.value = t('tokenGuardV2.totpManage.useJSON'); return
  }
  const content = format === 'json' ? JSON.stringify(data, null, 2) : includePassword.value ? `${data.email}----${data.password}----${data.secret}\n` : data.secret + '\n'
  const blob = new Blob([content], { type: format === 'json' ? 'application/json' : 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a'); a.href = url; a.download = `openai-2fa-${props.accountId}-${data.source}-${data.state}.${format}`
  document.body.appendChild(a); a.click(); a.remove(); setTimeout(() => URL.revokeObjectURL(url), 0)
}
onBeforeUnmount(close)
</script>
