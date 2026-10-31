<template>
  <section class="card space-y-3 p-4" data-testid="latency-observability">
    <h3 class="font-semibold">{{ t('latencyObservability.title') }}</h3>
    <p class="text-xs text-gray-500">{{ t('latencyObservability.scope') }}</p>
    <p class="text-xs text-gray-500">{{ t('latencyObservability.typedScope') }}</p>
    <p data-testid="reasoning-capability" class="text-sm">{{ t('latencyObservability.reasoningCapability') }}: {{ t('latencyObservability.unknown') }} · {{ t('latencyObservability.sourceNone') }}</p>
    <p v-if="ops" data-testid="socket-evidence" class="text-xs text-gray-500">{{ t('latencyObservability.socketScope') }}</p>
    <p v-if="ops" class="text-xs text-gray-500">{{ t('latencyObservability.opsPopulation') }}</p>
    <p data-testid="latency-e2e" class="text-sm">{{ t('latencyObservability.e2e') }}: {{ t('latencyObservability.notCollected') }} · {{ t('latencyObservability.sourceNone') }}</p>
    <p class="text-sm">{{ t('latencyObservability.clientUpload') }}: {{ t('latencyObservability.notCollected') }}</p>
    <p v-if="loading" role="status">{{ t('common.loading') }}</p>
    <p v-else-if="error" role="alert">{{ t('latencyObservability.error') }}</p>
    <p v-else-if="!report?.groups.length">{{ t('latencyObservability.empty') }}</p>
    <div v-else class="overflow-x-auto">
      <table class="w-full text-left text-xs">
        <thead><tr><th v-for="key in columns" :key="key" class="p-2">{{ t(`latencyObservability.${key}`) }}</th></tr></thead>
        <tbody><tr v-for="g in report.groups" :key="`${g.account_id}/${g.model}/${g.body_bucket}/${g.cache_bucket}/${g.reasoning_effort}`" data-testid="latency-group">
          <td class="p-2">#{{ g.account_id ?? '—' }} · {{ g.model ?? '—' }}</td>
          <td class="p-2">{{ g.body_bucket }}</td><td class="p-2">{{ g.cache_bucket }}</td><td class="p-2">{{ g.reasoning_effort }}</td>
          <td class="p-2">{{ measured(g.reasoning_first_avg_ms, g.reasoning_samples) }}</td>
          <td class="p-2">{{ measured(g.visible_text_first_avg_ms, g.text_samples) }}</td>
          <td class="p-2">{{ measured(g.response_byte_first_avg_ms, g.byte_samples) }}</td>
          <td class="p-2">{{ [g.visible_text_p50_ms, g.visible_text_p90_ms, g.visible_text_p99_ms].map(v => v == null ? '—' : `${v.toFixed(1)} ms`).join(' / ') }}</td>
          <td class="p-2">{{ g.requests }} / {{ g.traces }} / {{ g.attempts }}</td>
          <td class="p-2">{{ measured(g.body_upload_avg_ms, g.upload_samples) }}</td>
          <td class="p-2">{{ measured(g.post_upload_first_content_wait_avg_ms, g.wait_samples) }}</td>
          <td class="p-2">{{ measured(g.post_upload_first_visible_wait_avg_ms, g.visible_samples) }}</td>
          <td class="p-2">{{ g.failover_samples ? `${g.large_body_switches} / ${g.large_body_same_account_retries} (n=${g.failover_samples})` : t('latencyObservability.notCollected') }}</td>
          <td class="p-2">{{ measured(g.extra_upload_component_ms, g.cost_samples) }} / {{ measured(g.extra_wait_component_ms, g.cost_samples) }}</td>
        </tr></tbody>
      </table>
    </div>
  </section>
</template>
<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { getLatencyReport, type LatencyReport } from '@/api/admin/latencyObservability'
const props = defineProps<{ ops?: boolean; startDate?: string; endDate?: string; model?: string; platform?: string; userId?: number; apiKeyId?: number; accountId?: number; groupId?: number; refreshToken?: number }>()
const { t } = useI18n()
const report = ref<LatencyReport | null>(null), loading = ref(false), error = ref(false)
const columns = ['accountModel', 'body', 'cache', 'effort', 'reasoning_first_ms', 'visible_text_first_ms', 'response_byte_first_ms', 'textQuantiles', 'samples', 'upload', 'semantic', 'visible', 'switches', 'extra']
const measured = (value: number | null | undefined, count: number | undefined) => `${value == null ? t('latencyObservability.notCollected') : `${value.toFixed(1)} ms`} (n=${count ?? 0})`
let controller: AbortController | undefined
watch(() => [props.ops, props.startDate, props.endDate, props.model, props.platform, props.userId, props.apiKeyId, props.accountId, props.groupId, props.refreshToken], async () => {
  controller?.abort()
  const current = new AbortController(); controller = current
  loading.value = true; error.value = false; report.value = null
  try {
    const result = await getLatencyReport(!!props.ops, { start_date: props.startDate, end_date: props.endDate, model: props.model || undefined, platform: props.platform || undefined, user_id: props.userId || undefined, api_key_id: props.apiKeyId || undefined, account_id: props.accountId || undefined, group_id: props.groupId || undefined }, current.signal)
    if (controller === current && !current.signal.aborted) report.value = result
  } catch {
    if (controller === current && !current.signal.aborted) error.value = true
  } finally { if (controller === current) loading.value = false }
}, { immediate: true })
onUnmounted(() => { controller?.abort(); controller = undefined })
</script>
