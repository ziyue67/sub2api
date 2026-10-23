<template>
  <section class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600" data-test="cyber-evidence">
    <h3 class="font-semibold">{{ t('admin.riskControl.evidence.title') }}</h3>
    <p class="text-sm text-gray-500">{{ t('admin.riskControl.evidence.scope') }}</p>
    <p class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.riskControl.evidence.score') }}</p>
    <button type="button" class="btn btn-secondary" :disabled="busy" @click="load">{{ t('admin.riskControl.evidence.load') }}</button>
    <p v-if="error" role="alert">{{ error }}</p>
    <template v-if="record">
      <p class="break-all text-sm">UID {{ record.user_id ?? '-' }} · {{ record.request_id || '-' }} · {{ record.occurred_at }}</p>
      <p class="text-sm">{{ t('admin.riskControl.evidence.state') }}: {{ statusText(record.status) }}</p>
      <p v-if="!record.payload" class="text-sm">{{ t('admin.riskControl.evidence.noHistory') }}</p>
      <template v-else>
        <p class="text-sm">{{ record.payload.redaction_version }} · {{ record.payload.source }} · HTTP {{ record.payload.upstream_http_status ?? '-' }} · {{ record.payload.original_bytes }} bytes · {{ t(record.payload.truncated ? 'admin.riskControl.evidence.truncated' : 'admin.riskControl.evidence.complete') }}</p>
        <p class="text-sm">{{ t('admin.riskControl.evidence.expires') }}: {{ record.expires_at || '-' }}</p>
        <h4 class="font-medium">{{ t('admin.riskControl.evidence.input') }}</h4>
        <pre class="max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-950 p-4 text-sm text-gray-100">{{ formattedInput }}</pre>
        <h4 class="font-medium">{{ t('admin.riskControl.evidence.reason') }}</h4>
        <pre class="max-h-40 overflow-auto whitespace-pre-wrap break-words text-sm">{{ record.payload.upstream_reason || '-' }}</pre>
        <p class="text-xs text-gray-500">{{ t('admin.riskControl.evidence.redaction') }}</p>
      </template>
      <div class="flex gap-2">
        <button v-if="record.payload" type="button" class="btn btn-secondary" :disabled="busy" @click="download">{{ t('admin.riskControl.evidence.export') }}</button>
        <button type="button" class="btn btn-secondary" :disabled="busy || record.status === 'deleted'" @click="remove">{{ t('admin.riskControl.evidence.delete') }}</button>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { deleteEvidence, exportEvidence, getEvidence, type CyberEvidence } from '@/api/admin/cyberEvidence'
const props = defineProps<{ logId: number }>()
const { t, te } = useI18n()
const record = ref<CyberEvidence | null>(null)
const busy = ref(false)
const error = ref('')
watch(() => props.logId, () => { record.value = null; error.value = '' })
const formattedInput = computed(() => {
  const text = record.value?.payload?.input || ''
  try { return JSON.stringify(JSON.parse(text), null, 2) } catch { return text }
})
function statusText(state: string) {
  const key = `admin.riskControl.evidence.status.${state}`
  return te(key) ? t(key) : t('admin.riskControl.evidence.status.unavailable')
}
async function load() {
  busy.value = true; error.value = ''
  const id = props.logId
  try { const value = await getEvidence(id); if (id === props.logId) record.value = value }
  catch { error.value = t('admin.riskControl.evidence.failed') }
  finally { busy.value = false }
}
async function download() {
  busy.value = true; error.value = ''
  try {
    const blob = await exportEvidence(props.logId)
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a'); link.href = url; link.download = `cyber-evidence-${props.logId}.json`; link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch { error.value = t('admin.riskControl.evidence.failed') }
  finally { busy.value = false }
}
async function remove() {
  busy.value = true; error.value = ''
  const id = props.logId
  try { await deleteEvidence(id); if (id === props.logId) record.value = { ...record.value!, status: 'deleted', payload: undefined } }
  catch { error.value = t('admin.riskControl.evidence.failed') }
  finally { busy.value = false }
}
</script>
