<template>
  <section class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-700" :aria-label="t('adminAccessKeys.legacyIP.title')">
    <h3 class="font-medium">{{ t('adminAccessKeys.legacyIP.title') }}</h3>
    <p class="text-sm text-gray-500">{{ t('adminAccessKeys.legacyIP.scope') }}</p>
    <p class="text-sm text-gray-500">{{ t('adminAccessKeys.legacyIP.proxyHint') }}</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="loading">{{ t('common.loading') }}</p>
    <template v-else-if="loaded">
      <p class="text-sm">{{ t('adminAccessKeys.legacyIP.currentIP', { ip: currentIP || '—' }) }}</p>
      <p data-test="saved-status" class="text-sm text-gray-500">{{ t('adminAccessKeys.legacyIP.savedMode', { mode: t(saved.enabled ? 'adminAccessKeys.legacyIP.on' : 'adminAccessKeys.legacyIP.off'), count: saved.allowed_ips.length }) }}</p>
      <label class="flex items-center gap-2">
        <input v-model="enabled" data-test="enabled" type="checkbox" :disabled="busy" />
        {{ t('adminAccessKeys.legacyIP.enable') }}
      </label>
      <label class="block">
        <span class="mb-1 block text-sm">{{ t('adminAccessKeys.legacyIP.entries') }}</span>
        <textarea v-model="text" data-test="ips" rows="5" class="input w-full font-mono" :disabled="busy" placeholder="203.0.113.10&#10;2001:db8::/64" />
      </label>
      <p class="text-sm text-gray-500">{{ t(dirty ? 'adminAccessKeys.legacyIP.unsaved' : 'adminAccessKeys.legacyIP.saved') }}</p>
      <button type="button" data-test="save" class="btn btn-primary btn-sm" :disabled="busy || !dirty" @click="save">{{ t('common.save') }}</button>
    </template>
    <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || loading" @click="load">{{ t('adminAccessKeys.refresh') }}</button>
    <TotpStepUpDialog :controller="stepUp" />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getLegacyAdminKeyIPPolicy, updateLegacyAdminKeyIPPolicy } from '@/api/admin/legacyAdminKeyIP'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'

const { t } = useI18n()
const stepUp = useStepUp()
const enabled = ref(false)
const text = ref('')
const currentIP = ref('')
const loading = ref(false)
const loaded = ref(false)
const busy = ref(false)
const error = ref('')
const saved = ref({ enabled: false, allowed_ips: [] as string[] })
const entries = computed(() => text.value.split(/\r?\n/).map(v => v.trim()).filter(Boolean))
const dirty = computed(() => enabled.value !== saved.value.enabled || JSON.stringify(entries.value) !== JSON.stringify(saved.value.allowed_ips))
function report(e: unknown) {
  error.value = (e as { response?: { data?: { message?: string } } })?.response?.data?.message || t('adminAccessKeys.legacyIP.failed')
}
function apply(p: Awaited<ReturnType<typeof getLegacyAdminKeyIPPolicy>>) {
  saved.value = { enabled: p.enabled, allowed_ips: [...p.allowed_ips] }
  enabled.value = p.enabled
  text.value = p.allowed_ips.join('\n')
  currentIP.value = p.current_ip
  loaded.value = true
}
async function load() {
  loading.value = true
  error.value = ''
  try { apply(await getLegacyAdminKeyIPPolicy()) } catch (e) { report(e) } finally { loading.value = false }
}
async function save() {
  if (entries.value.length > 100 || (enabled.value && entries.value.length === 0)) {
    error.value = t('adminAccessKeys.legacyIP.invalid')
    return
  }
  busy.value = true
  error.value = ''
  // Snapshot the complete policy so a TOTP retry submits the same settings.
  const policy = { enabled: enabled.value, allowed_ips: [...entries.value] }
  try { apply(await stepUp.run(() => updateLegacyAdminKeyIPPolicy(policy))) }
  catch (e) { if (!isStepUpCancelled(e)) report(e) }
  finally { busy.value = false }
}
onMounted(load)
</script>
