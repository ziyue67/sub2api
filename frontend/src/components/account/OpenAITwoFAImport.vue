<template>
  <div class="space-y-4">
    <p class="input-hint">{{ t('tokenGuard.twoFA.hint') }}</p>
    <a href="/admin/token-guard" target="_blank" rel="noopener noreferrer" class="text-primary-600">
      {{ t('tokenGuard.twoFA.settings') }}
    </a>
    <template v-if="!rows.length">
      <label for="two-fa-credentials" class="input-label">{{ t('tokenGuard.twoFA.credentials') }}</label>
      <textarea id="two-fa-credentials" v-model="raw" class="input font-mono" rows="6"
        autocomplete="off" autocapitalize="off" :spellcheck="false" :disabled="busy"
        :placeholder="t('tokenGuard.twoFA.placeholder')" />
    </template>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <ul v-if="rows.length" class="space-y-2" aria-live="polite">
      <li v-for="(row, index) in rows" :key="row.entry.email" class="flex justify-between gap-4 text-sm">
        <span>{{ index + 1 }}. {{ row.entry.email }}</span>
        <span>{{ t(`tokenGuard.twoFA.states.${row.status}`) }}</span>
      </li>
    </ul>
    <div class="flex gap-3">
      <button v-if="!busy && (!rows.length || hasPending)" type="button" class="btn btn-primary" @click="run">
        {{ t(rows.length ? 'tokenGuard.twoFA.retry' : 'tokenGuard.twoFA.start') }}
      </button>
      <button v-if="busy" type="button" class="btn btn-secondary" :disabled="stopRequested" @click="stopRequested = true">
        {{ t('tokenGuard.twoFA.stop') }}
      </button>
      <button v-if="!busy && rows.length" type="button" class="btn btn-secondary" @click="reset">
        {{ t('tokenGuard.twoFA.reset') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { deleteTwoFALogin, getTwoFALogin, parseTwoFALoginText, startTwoFALogin } from '@/api/admin/accountTokenGuard'
import type { TokenGuardReloginAccount } from '@/api/admin/accountTokenGuard'

const props = defineProps<{
  importCredential: (credential: Record<string, unknown>, email: string, login: TokenGuardReloginAccount) => Promise<'created' | 'skipped'>
}>()
const emit = defineEmits<{ busy: [value: boolean] }>()
const { t } = useI18n()
type Row = {
  entry: TokenGuardReloginAccount
  status: 'pending' | 'login' | 'importing' | 'created' | 'skipped' | 'failed' | 'importFailed'
  credential?: Record<string, unknown>
  jobId?: string
}
const raw = ref('')
const error = ref('')
const rows = ref<Row[]>([])
const busy = ref(false)
const stopRequested = ref(false)
const hasPending = computed(() => rows.value.some(row => row.status !== 'created' && row.status !== 'skipped'))
let disposed = false

async function discardJob(row: Row) {
  if (!row.jobId) return
  const id = row.jobId
  row.jobId = undefined
  // Server expiry also cleans up jobs if the browser loses its connection.
  await deleteTwoFALogin(id).catch(() => {})
}

async function run() {
  if (busy.value) return
  error.value = ''
  if (!rows.value.length) {
    try {
      rows.value = parseTwoFALoginText(raw.value).map(entry => ({ entry, status: 'pending' }))
      raw.value = ''
    } catch {
      error.value = t('tokenGuard.twoFA.invalid')
      return
    }
  }
  busy.value = true
  stopRequested.value = false
  emit('busy', true)
  try {
    for (const row of rows.value) {
      if (stopRequested.value || disposed) break
      if (row.status === 'created' || row.status === 'skipped') continue
      try {
        if (!row.credential) {
          row.status = 'login'
          // Keep the job ID on polling failures; retry resumes the same login.
          if (!row.jobId) row.jobId = (await startTwoFALogin(row.entry)).id
          let job = await getTwoFALogin(row.jobId)
          while (job.status === 'running' && !stopRequested.value && !disposed) {
            await new Promise(resolve => setTimeout(resolve, 1500))
            if (stopRequested.value || disposed) break
            job = await getTwoFALogin(row.jobId)
          }
          if (stopRequested.value || disposed) {
            await discardJob(row)
            row.status = 'pending'
            break
          }
          if (job.status !== 'succeeded' || !job.credential) {
            await discardJob(row)
            throw new Error('login_failed')
          }
          row.credential = job.credential
          await discardJob(row)
        }
        if (stopRequested.value || disposed) { row.status = 'pending'; break }
        row.status = 'importing'
        // Keep the input in memory until encrypted credential-operations
        // enrollment succeeds. An import/enrollment retry reuses this login.
        row.status = await props.importCredential(row.credential, row.entry.email, { ...row.entry })
        row.credential = undefined
        row.entry.password = ''
        row.entry.mfa_secret = ''
      } catch (cause: unknown) {
        const status = (cause as { status?: number; response?: { status?: number } })?.status ??
          (cause as { response?: { status?: number } })?.response?.status
        if (status === 404) row.jobId = undefined
        row.status = row.credential ? 'importFailed' : 'failed'
      }
    }
  } finally {
    busy.value = false
    emit('busy', false)
  }
}

function reset() {
  for (const row of rows.value) void discardJob(row)
  rows.value = []
  raw.value = ''
  error.value = ''
}

onBeforeUnmount(() => {
  disposed = true
  stopRequested.value = true
  reset()
})
</script>
