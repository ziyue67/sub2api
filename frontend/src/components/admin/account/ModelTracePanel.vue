<template>
  <div class="space-y-4" data-testid="modeltrace-panel">
    <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('modelTrace.intro') }}</p>
    <div class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200">
      {{ t('modelTrace.limitation') }}
      <p v-if="account.platform === 'anthropic' && account.type !== 'apikey'" class="mt-2" data-testid="modeltrace-system-warning">{{ t('modelTrace.claudeSystemWarning') }}</p>
    </div>
    <p v-if="!supported" class="text-sm text-amber-700 dark:text-amber-300">{{ t('modelTrace.unsupported') }}</p>
    <div class="grid gap-4 sm:grid-cols-2">
      <TestModelSelect id="modeltrace-model" :model-value="modelId" :options="modelOptions" :disabled="running"
        :label="t('modelTrace.model')" :hint="t('modelTrace.modelHint')" @update:model-value="emit('update:modelId', $event)" />
      <div>
        <label class="input-label mb-1.5 block">{{ t('modelTrace.effort') }}</label>
        <Select v-model="effort" :options="effortOptions" :disabled="running" />
        <p class="input-hint">{{ t('modelTrace.effortHint') }}</p>
      </div>
    </div>
    <div class="flex flex-wrap items-center gap-3">
      <button v-if="!running" type="button" class="btn btn-primary" data-testid="modeltrace-start" :disabled="!supported || !modelId.trim()" @click="start">{{ t('modelTrace.start') }}</button>
      <button v-else type="button" class="btn btn-secondary" data-testid="modeltrace-cancel" @click="cancel">{{ t('modelTrace.cancel') }}</button>
      <span v-if="running" class="text-sm text-primary-600 dark:text-primary-300" role="status">{{ t('modelTrace.progress', { count: samples.length }) }}</span>
    </div>
    <p v-if="error" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
    <p v-if="saveFailed" class="text-xs text-amber-700 dark:text-amber-300">{{ t('modelTrace.saveFailed') }}</p>

    <section v-if="result" class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600" data-testid="modeltrace-result">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h3 class="font-semibold text-gray-900 dark:text-gray-100">{{ t(`modelTrace.status.${result.status}`) }}</h3>
        <span class="text-xs text-gray-500">{{ result.analysis?.used_outputs || 0 }} / 3 · {{ result.requested_model }} · {{ result.reasoning_effort || t('modelTrace.providerDefault') }}</span>
      </div>
      <p v-if="result.analysis" class="text-sm text-gray-800 dark:text-gray-200">
        {{ t('modelTrace.prediction') }} <strong>{{ result.analysis.prediction }}</strong> · {{ percent(result.analysis.probability) }}
      </p>
      <p v-if="result.disagreement" class="text-sm text-amber-700 dark:text-amber-300">{{ t('modelTrace.disagreement') }}</p>
      <p v-if="result.status === 'partial'" class="text-xs text-amber-700 dark:text-amber-300">{{ t('modelTrace.partialHint') }}</p>
      <div v-if="result.analysis" class="space-y-2">
        <div v-for="candidate in result.analysis.candidates.slice(0, 5)" :key="candidate.model" class="text-xs">
          <div class="mb-1 flex justify-between gap-3 text-gray-700 dark:text-gray-300"><span>{{ candidate.model }}</span><span>{{ percent(candidate.probability) }}</span></div>
          <div class="h-1.5 overflow-hidden rounded bg-gray-100 dark:bg-dark-700"><div class="h-full bg-primary-500" :style="{ width: `${candidate.probability * 100}%` }" /></div>
        </div>
        <p class="text-xs text-gray-500">{{ t('modelTrace.families') }} {{ result.analysis.families.map(item => `${item.family} ${percent(item.probability)}`).join(' · ') }}</p>
      </div>
      <details class="text-xs text-gray-500 dark:text-gray-400">
        <summary class="cursor-pointer">{{ t('modelTrace.bank') }}</summary>
        <p class="mt-2 break-all">{{ result.bank.algorithm }} · {{ result.bank.source_commit }} · SHA256 {{ result.bank.sha256 }}</p>
        <p class="mt-1">{{ result.bank.models.join(', ') }}</p>
      </details>
    </section>

    <div v-if="samples.length || (running && challenges.length)" class="space-y-3">
      <article v-for="(challenge, index) in challenges" :key="challenge.id" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
        <header class="flex flex-wrap justify-between gap-2 text-sm text-gray-800 dark:text-gray-200">
          <span>{{ t('modelTrace.sample', { index: index + 1 }) }} · {{ challenge.expected_count }}</span>
          <span v-if="samples[index]">{{ samples[index]!.accepted ? t('modelTrace.accepted') : t(`modelTrace.errors.${samples[index]!.error || 'generation_failed'}`) }}</span>
          <span v-else class="text-gray-500">{{ t('modelTrace.pending') }}</span>
        </header>
        <template v-if="samples[index]">
          <p class="mt-2 text-xs text-gray-500">{{ samples[index]!.mapped_model || '—' }} · {{ samples[index]!.environment.protocol || '—' }} · {{ t('modelTrace.numbers', { count: samples[index]!.parsed_numbers, minimum: samples[index]!.minimum_numbers }) }} · {{ (samples[index]!.latency_ms / 1000).toFixed(1) }} s</p>
          <p v-if="samples[index]!.analysis" class="mt-1 text-sm text-gray-800 dark:text-gray-200">{{ samples[index]!.analysis!.prediction }} · {{ percent(samples[index]!.analysis!.probability) }}</p>
          <p class="mt-1 text-xs text-gray-500">{{ t('modelTrace.system') }} {{ t(`modelTrace.systems.${samples[index]!.environment.system_prompt || 'none'}`) }}</p>
        </template>
        <details class="mt-2 text-xs text-gray-600 dark:text-gray-400">
          <summary class="cursor-pointer">{{ t('modelTrace.raw') }}</summary>
          <pre class="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-words">{{ challenge.prompt }}</pre>
          <pre v-if="samples[index]?.text" class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words">{{ samples[index]!.text }}</pre>
        </details>
      </article>
    </div>

    <section v-if="history.length" class="space-y-2">
      <div class="flex items-center justify-between gap-3">
        <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-200">{{ t('modelTrace.history') }}</h3>
        <button type="button" class="text-xs text-gray-500" :disabled="running" @click="clearHistory">{{ t('modelTrace.clear') }}</button>
      </div>
      <p class="text-xs text-gray-500">{{ t('modelTrace.historyHint') }}</p>
      <button v-for="record in history" :key="record.created_at" type="button" :disabled="running" class="flex w-full flex-wrap justify-between gap-2 rounded-lg border border-gray-200 p-3 text-left text-xs text-gray-700 disabled:opacity-50 dark:border-dark-600 dark:text-gray-300" @click="showRecord(record)">
        <span>{{ new Date(record.created_at).toLocaleString() }} · {{ record.requested_model }}</span>
        <span>{{ record.analysis?.prediction || t('modelTrace.status.insufficient') }} · {{ record.analysis?.used_outputs || 0 }}/3</span>
      </button>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import { runModelTrace, type ModelTraceChallenge, type ModelTraceResult, type ModelTraceSample } from '@/api/admin/modelTrace'
import Select from '@/components/common/Select.vue'
import TestModelSelect from './TestModelSelect.vue'

const props = defineProps<{
  account: Account
  modelId: string
  modelOptions: Array<{ value: string; label: string }>
  reasoningLevels: string[]
}>()
const emit = defineEmits<{ (event: 'update:modelId', value: string): void; (event: 'running', value: boolean): void }>()
const { t } = useI18n()
const effort = ref('')
const running = ref(false)
const challenges = ref<ModelTraceChallenge[]>([])
const samples = ref<ModelTraceSample[]>([])
const result = ref<ModelTraceResult | null>(null)
const error = ref('')
const saveFailed = ref(false)
const history = ref<ModelTraceResult[]>([])
let controller: AbortController | undefined
const storageKey = computed(() => `sub2api-modeltrace-test:${props.account.id}`)
const supported = computed(() => ['openai', 'anthropic'].includes(props.account.platform) && ['apikey', 'oauth', 'setup-token'].includes(props.account.type))
const effortOptions = computed(() => [{ value: '', label: t('modelTrace.providerDefault') }, ...props.reasoningLevels.map(value => ({ value, label: value }))])
const percent = (value: number) => `${(value * 100).toFixed(1)}%`

function showRecord(record: ModelTraceResult) {
  result.value = record
  samples.value = record.samples
  challenges.value = record.samples.map(sample => sample.challenge)
  error.value = ''
}

function clearHistory() {
  history.value = []
  try { localStorage.removeItem(storageKey.value); saveFailed.value = false } catch { saveFailed.value = true }
}

function cancel() { controller?.abort() }

async function start() {
  if (running.value || !supported.value || !props.modelId.trim()) return
  const activeController = new AbortController()
  controller = activeController
  const accountId = props.account.id
  const key = storageKey.value
  running.value = true
  emit('running', true)
  result.value = null
  samples.value = []
  challenges.value = []
  error.value = ''
  try {
    const completed = await runModelTrace(accountId, props.modelId.trim(), effort.value, activeController.signal, event => {
      if (activeController.signal.aborted || props.account.id !== accountId) return
      if (event.type === 'modeltrace_start') challenges.value = event.data.challenges
      if (event.type === 'modeltrace_sample') samples.value = [...samples.value, event.data]
    })
    if (activeController.signal.aborted || props.account.id !== accountId) return
    showRecord(completed)
    history.value = [completed, ...history.value].slice(0, 5)
    try { localStorage.setItem(key, JSON.stringify(history.value)); saveFailed.value = false } catch { saveFailed.value = true }
  } catch (failure) {
    if (props.account.id !== accountId) return
    const status = failure instanceof Error ? failure.message.match(/^HTTP (\d+)$/)?.[1] : undefined
    error.value = activeController.signal.aborted ? t('modelTrace.cancelled') : status ? t('modelTrace.httpError', { status }) : t('modelTrace.requestFailed')
  } finally {
    if (controller === activeController) {
      controller = undefined
      running.value = false
      emit('running', false)
    }
  }
}

watch(() => props.modelId, () => { effort.value = '' })
watch(storageKey, () => {
  cancel()
  result.value = null
  samples.value = []
  challenges.value = []
  error.value = ''
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(storageKey.value) || '[]')
    history.value = Array.isArray(saved) ? saved.filter(record => record?.bank?.sha256 && Array.isArray(record.samples) && ['complete', 'partial', 'insufficient'].includes(record.status)).slice(0, 5) : []
  } catch { history.value = [] }
}, { immediate: true })

onBeforeUnmount(() => { cancel(); emit('running', false) })
</script>
