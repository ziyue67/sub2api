<template>
  <fieldset class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600" :disabled="busy">
    <legend class="px-1 font-medium">{{ t('admin.moderationUsers.title') }} · UID {{ userId }}</legend>
    <p class="text-sm text-gray-500">{{ t('admin.moderationUsers.scope') }}</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <button type="button" class="btn btn-secondary" @click="load">{{ t('common.refresh') }}</button>
    <template v-if="state">
      <p data-test="current-state" class="text-sm">{{ t('admin.moderationUsers.current', { count: state.current_count, status: state.status, generation: state.generation }) }}</p>
      <p class="break-words text-xs text-gray-500">{{ t('admin.moderationUsers.lastAction') }}: {{ state.reason || '—' }} · UID {{ state.actor_id ?? '—' }} · {{ state.updated_at ? new Date(state.updated_at).toLocaleString() : '—' }}</p>
      <label class="flex items-start gap-2 text-sm">
        <input v-model="exempt" type="checkbox" data-test="exempt" class="mt-1" />
        <span>{{ t('admin.moderationUsers.exemption') }}</span>
      </label>
      <label class="flex items-start gap-2 text-sm">
        <input v-model="keywordExempt" type="checkbox" data-test="keyword-exempt" class="mt-1" />
        <span>{{ t('admin.moderationUsers.keywordExemption') }}</span>
      </label>
      <label class="flex items-start gap-2 text-sm">
        <input v-model="apiAuditExempt" type="checkbox" data-test="api-audit-exempt" class="mt-1" />
        <span>{{ t('admin.moderationUsers.apiAuditExemption') }}</span>
      </label>
      <label class="flex items-start gap-2 text-sm">
        <input v-model="emailExempt" type="checkbox" data-test="email-exempt" class="mt-1" />
        <span>{{ t('admin.moderationUsers.emailExemption') }}<small class="mt-1 block text-gray-500">{{ t('admin.moderationUsers.emailScope') }}</small></span>
      </label>
      <label class="block text-sm">{{ t('admin.moderationUsers.reason') }}
        <input v-model.trim="reason" class="input mt-1" maxlength="500" data-test="reason" />
      </label>
      <label class="flex items-start gap-2 text-sm">
        <input v-model="confirmed" type="checkbox" class="mt-1" data-test="confirm" />
        <span>{{ t('admin.moderationUsers.confirm') }}</span>
      </label>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn btn-secondary" data-test="save-exemption" :disabled="!ready || !exemptionsChanged" @click="apply('exemption')">{{ t('admin.moderationUsers.saveExemption') }}</button>
        <button type="button" class="btn btn-secondary" data-test="reset" :disabled="!ready" @click="apply('reset')">{{ t('admin.moderationUsers.reset') }}</button>
        <button type="button" class="btn btn-secondary" data-test="unban" :disabled="!ready || !state.owned_disable" @click="apply('unban')">{{ t('admin.moderationUsers.unban') }}</button>
      </div>
      <p v-if="state.status !== 'active' && !state.owned_disable" class="text-sm text-amber-600">{{ t('admin.moderationUsers.otherSource') }}</p>
      <p class="text-xs text-gray-500">{{ t('admin.moderationUsers.independent') }}</p>
      <p v-if="success" role="status" class="text-sm text-emerald-600">{{ t('admin.moderationUsers.saved') }}</p>
      <section class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600" :aria-label="t('admin.moderationUsers.enableUser')">
        <h3 class="font-medium">{{ t('admin.moderationUsers.enableUser') }}</h3>
        <p data-test="enable-scope" class="text-sm text-gray-500">{{ t('admin.moderationUsers.enableScope') }}</p>
        <p v-if="state.status === 'active'" data-test="already-active" class="text-sm">{{ t('admin.moderationUsers.alreadyActive') }}</p>
        <template v-else-if="state.status === 'disabled'">
          <label class="block text-sm">{{ t('admin.moderationUsers.reason') }}
            <input v-model.trim="enableReason" class="input mt-1" maxlength="500" data-test="enable-reason" />
          </label>
          <label class="flex items-start gap-2 text-sm">
            <input v-model="enableConfirmed" type="checkbox" class="mt-1" data-test="enable-confirm" />
            <span>{{ t('admin.moderationUsers.enableConfirm', { userId }) }}</span>
          </label>
        </template>
        <button type="button" class="btn btn-secondary" data-test="enable-user" :disabled="!enableReady" @click="enableUser">{{ t('admin.moderationUsers.enableUser') }}</button>
        <p v-if="enableSuccess" role="status" data-test="enable-success" class="text-sm text-emerald-600">{{ t('admin.moderationUsers.enableSuccess') }}</p>
      </section>
    </template>
  </fieldset>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getModerationUser, manageModerationUser, type ModerationUserAction, type ModerationUserState } from '@/api/admin/riskControl'
import { update as updateUser } from '@/api/admin/users'
const props = defineProps<{ userId: number }>()
const emit = defineEmits<{ changed: [state: ModerationUserState] }>()
const { t } = useI18n()
const state = ref<ModerationUserState | null>(null)
const exempt = ref(false)
const keywordExempt = ref(false)
const apiAuditExempt = ref(false)
const emailExempt = ref(false)
const exemptionsChanged = computed(() => state.value && (exempt.value !== state.value.auto_ban_exempt || keywordExempt.value !== (state.value.keyword_exempt ?? false) || apiAuditExempt.value !== (state.value.api_audit_exempt ?? false) || emailExempt.value !== (state.value.email_exempt ?? false)))
function readState(data: ModerationUserState) {
  state.value = data
  exempt.value = data.auto_ban_exempt
  keywordExempt.value = data.keyword_exempt ?? false
  apiAuditExempt.value = data.api_audit_exempt ?? false
  emailExempt.value = data.email_exempt ?? false
}
const reason = ref('')
const confirmed = ref(false)
const busy = ref(false)
const error = ref('')
const success = ref(false)
const enableReason = ref('')
const enableConfirmed = ref(false)
const enableSuccess = ref(false)
let loadVersion = 0
const ready = computed(() => !busy.value && confirmed.value && reason.value.length > 0)
const enableReady = computed(() => !busy.value && state.value?.status === 'disabled' && enableConfirmed.value && enableReason.value.trim().length > 0 && [...enableReason.value].length <= 500)
function message(err: unknown) {
  const value = err as { response?: { data?: { message?: string } }; message?: string }
  return value.response?.data?.message || value.message || t('admin.moderationUsers.failed')
}
async function load() {
  const version = ++loadVersion
  busy.value = true; state.value = null; error.value = ''; success.value = false
  confirmed.value = false; reason.value = ''
  enableConfirmed.value = false; enableReason.value = ''; enableSuccess.value = false
  try {
    const data = await getModerationUser(props.userId)
    if (version === loadVersion) { readState(data) }
  } catch (err) { if (version === loadVersion) error.value = message(err) }
  finally { if (version === loadVersion) busy.value = false }
}
async function apply(action: ModerationUserAction['action']) {
  if (!state.value || !ready.value) return
  const version = loadVersion
  busy.value = true; error.value = ''; success.value = false
  enableConfirmed.value = false; enableSuccess.value = false
  try {
    const data = await manageModerationUser(props.userId, { action, version: state.value.version, reason: reason.value, confirm: confirmed.value, ...(action === 'exemption' ? { auto_ban_exempt: exempt.value, keyword_exempt: keywordExempt.value, api_audit_exempt: apiAuditExempt.value, email_exempt: emailExempt.value } : {}) })
    if (version !== loadVersion) return
    readState(data); confirmed.value = false; reason.value = ''; success.value = true
    emit('changed', data)
  } catch (err) { if (version === loadVersion) { error.value = message(err); confirmed.value = false } }
  finally { if (version === loadVersion) busy.value = false }
}
async function enableUser() {
  if (!enableReady.value) return
  const version = loadVersion
  const userId = props.userId
  const actionReason = enableReason.value.trim()
  busy.value = true; error.value = ''; success.value = false; enableSuccess.value = false
  confirmed.value = false
  try {
    // Recheck via the moderation projection, without loading user credentials or profile data.
    const before = await getModerationUser(userId)
    if (version !== loadVersion) return
    readState(before)
    if (before.status === 'active') { emit('changed', before); return }
    if (before.status !== 'disabled') throw new Error(t('admin.moderationUsers.failed'))

    let writeError: unknown
    try {
      // Same administrator path as User management. Never send moderation resets or exemptions.
      await updateUser(userId, { status: 'active', reason: actionReason })
    } catch (err) { writeError = err }
    if (version !== loadVersion) return
    // A failed write may still have committed. Read current state, without reporting success.
    state.value = null
    const after = await getModerationUser(userId)
    if (version !== loadVersion) return
    readState(after); emit('changed', after)
    if (writeError) throw writeError
    if (after.status !== 'active') throw new Error(t('admin.moderationUsers.enableStillDisabled'))
    enableSuccess.value = true; enableReason.value = ''
  } catch (err) { if (version === loadVersion) error.value = message(err) }
  finally { if (version === loadVersion) { enableConfirmed.value = false; busy.value = false } }
}
watch(() => props.userId, load, { immediate: true })
</script>
