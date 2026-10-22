<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600 lg:col-span-2">
    <h3 class="font-semibold">{{ t('admin.riskControl.evidence.settings') }}</h3>
    <p class="text-sm text-gray-500">{{ t('admin.riskControl.evidence.policy') }}</p>
    <button type="button" class="btn btn-secondary" :disabled="busy" @click="load">{{ t('admin.riskControl.evidence.loadConfig') }}</button>
    <p v-if="message" role="status">{{ message }}</p>
    <template v-if="config && runtime">
      <label class="flex items-center gap-2"><input v-model="config.enabled" type="checkbox" />{{ t('admin.riskControl.evidence.enabled') }}</label>
      <label class="block">{{ t('admin.riskControl.evidence.retention') }}<input v-model.number="config.retention_days" type="number" min="1" max="30" class="input mt-1" /></label>
      <p class="text-sm">{{ t(runtime.encryption_ready ? 'admin.riskControl.evidence.keyReady' : 'admin.riskControl.evidence.keyMissing') }}</p>
      <p class="text-sm">{{ t('admin.riskControl.evidence.runtime', { saved: runtime.saved, failed: runtime.failed, dropped: runtime.dropped, depth: runtime.queue_depth, capacity: runtime.queue_capacity, statusFailed: runtime.status_write_failed, cleanupFailed: runtime.cleanup_failed, configFailed: runtime.config_load_failed }) }}</p>
      <button type="button" class="btn btn-primary" :disabled="busy || !valid" @click="save">{{ t('admin.riskControl.evidence.save') }}</button>
    </template>
  </section>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getEvidenceConfig, updateEvidenceConfig, type EvidenceConfig, type EvidenceRuntime } from '@/api/admin/cyberEvidence'
const { t } = useI18n()
const config = ref<EvidenceConfig | null>(null)
const runtime = ref<EvidenceRuntime | null>(null)
const message = ref('')
const busy = ref(false)
const valid = computed(() => Number.isInteger(config.value?.retention_days) && (config.value?.retention_days ?? 0) >= 1 && (config.value?.retention_days ?? 31) <= 30)
async function load() {
  busy.value = true; message.value = ''
  try { const data = await getEvidenceConfig(); config.value = data.config; runtime.value = data.runtime }
  catch { message.value = t('admin.riskControl.evidence.failed') }
  finally { busy.value = false }
}
async function save() {
  if (!config.value || !valid.value) return
  busy.value = true; message.value = ''
  try { config.value = await updateEvidenceConfig(config.value); message.value = t('admin.riskControl.evidence.saved') }
  catch { message.value = t('admin.riskControl.evidence.failed') }
  finally { busy.value = false }
}
</script>
