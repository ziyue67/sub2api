<template>
  <AppLayout>
    <div class="space-y-6">
      <SmartOpsNav />
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div><h1 class="text-2xl font-semibold text-gray-900 dark:text-gray-100">{{ t('controlledExperiments.title') }}</h1><p class="mt-2 max-w-4xl text-sm leading-6 text-gray-500">{{ t('controlledExperiments.intro') }}</p></div>
        <button class="btn btn-secondary" :disabled="loading || busy" @click="refresh">{{ t('controlledExperiments.refresh') }}</button>
      </header>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>

      <details class="card" :open="!report">
        <summary class="cursor-pointer p-5 font-semibold">{{ t('controlledExperiments.newRun') }}</summary>
        <form class="space-y-5 border-t border-gray-100 p-5 dark:border-dark-700" @submit.prevent="create">
          <div class="grid gap-4 md:grid-cols-3">
            <label class="experiment-field">{{ t('controlledExperiments.name') }}<input v-model="form.name" required maxlength="100" class="input" /></label>
            <label class="experiment-field">{{ t('controlledExperiments.model') }}<input v-model="form.model" required maxlength="100" class="input font-mono" /></label>
            <label class="experiment-field">{{ t('controlledExperiments.effort') }}<select v-model="form.reasoning_effort" class="input"><option v-for="effort in efforts" :key="effort">{{ effort }}</option></select></label>
            <label class="experiment-field">{{ t('controlledExperiments.split') }}<select v-model="form.split" class="input" data-testid="experiment-split"><option value="screen">{{ t('controlledExperiments.screen') }}</option><option value="confirm">{{ t('controlledExperiments.confirm') }}</option></select></label>
            <label class="experiment-field">{{ t('controlledExperiments.repetitions') }}<input v-model.number="form.repetitions" type="number" min="1" max="3" required class="input" /></label>
            <label class="experiment-field">{{ t('controlledExperiments.timeout') }}<input v-model.number="form.timeout_seconds" type="number" min="30" max="720" required class="input" /></label>
          </div>
          <fieldset class="space-y-3">
            <legend class="mb-2 text-sm font-semibold">{{ t('controlledExperiments.routes') }}</legend>
            <div v-for="(route, index) in form.routes" :key="index" class="flex flex-wrap items-center gap-3">
              <span class="w-6 text-sm text-gray-400">{{ index + 1 }}</span>
              <select v-model.number="route.account_id" class="input min-w-48 flex-1" required :aria-label="t('controlledExperiments.account')" data-testid="experiment-account">
                <option :value="0" disabled>{{ t('controlledExperiments.account') }}</option>
                <option v-for="account in accounts" :key="account.id" :value="account.id">#{{ account.id }} · {{ account.name }}</option>
              </select>
              <select v-model="route.channel" class="input min-w-48 flex-1" :aria-label="t('controlledExperiments.channel')"><option v-for="channel in channels" :key="channel" :value="channel">{{ channelLabel(channel) }}</option></select>
              <button type="button" class="btn btn-secondary" :disabled="form.routes.length === 1" @click="form.routes.splice(index, 1)">{{ t('controlledExperiments.remove') }}</button>
            </div>
            <button type="button" class="btn btn-secondary" :disabled="form.routes.length >= 4" @click="form.routes.push({ account_id: 0, channel: 'native_http' })">{{ t('controlledExperiments.addRoute') }}</button>
            <p class="text-xs leading-5 text-gray-500">{{ t('controlledExperiments.channelHint') }}</p>
            <p v-if="form.routes.some(route => route.channel === 'native_ws')" class="rounded-lg bg-amber-50 p-3 text-sm leading-6 text-amber-800 dark:bg-amber-900/20 dark:text-amber-200" data-testid="experiment-ws-prerequisite">{{ t('controlledExperiments.wsPrerequisiteHint') }}</p>
          </fieldset>
          <fieldset>
            <legend class="mb-3 text-sm font-semibold">{{ t('controlledExperiments.tasks') }} · {{ t('controlledExperiments.selected', { count: selectedTasks.length }) }}</legend>
            <div class="mb-3 flex gap-3 text-sm"><button type="button" class="text-primary-600" @click="selectAll">{{ t('controlledExperiments.selectAll') }}</button><button type="button" class="text-gray-500" @click="form.task_ids = []">{{ t('controlledExperiments.selectNone') }}</button></div>
            <div class="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
              <div v-for="task in splitTasks" :key="task.id" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
                <label class="flex items-center gap-2 text-sm"><input v-model="form.task_ids" type="checkbox" :value="task.id" /><span class="font-medium">{{ task.id }}</span><span class="ml-auto text-xs text-gray-500">{{ t(`controlledExperiments.${task.category}`) }}</span></label>
                <details class="mt-2 text-xs text-gray-500"><summary class="cursor-pointer">{{ task.family }} · {{ t('controlledExperiments.turns', { count: task.max_turns }) }}</summary><pre class="experiment-content mt-2 max-h-52">{{ task.prompt }}</pre></details>
              </div>
            </div>
          </fieldset>
          <div class="rounded-lg bg-gray-50 p-4 dark:bg-dark-800">
            <label class="experiment-field max-w-xs">{{ t('controlledExperiments.maxCalls') }}<input v-model.number="form.max_calls" type="number" min="1" max="300" required class="input" data-testid="experiment-budget" /></label>
            <p class="mt-3 text-sm" data-testid="experiment-planned">{{ t('controlledExperiments.planned', { count: plannedCalls }) }}</p>
            <p v-if="form.max_calls < plannedCalls" class="mt-2 text-sm text-amber-700 dark:text-amber-300">{{ t('controlledExperiments.limited') }}</p>
            <p class="mt-2 text-xs leading-5 text-gray-500">{{ t('controlledExperiments.budgetHint') }}</p>
          </div>
          <div class="flex flex-wrap items-center gap-4"><button type="submit" class="btn btn-primary" :disabled="busy || loading || !validForm" data-testid="experiment-create">{{ t('controlledExperiments.save') }}</button><p class="text-xs text-gray-500">{{ t('controlledExperiments.draftHint') }}</p></div>
        </form>
      </details>

      <div class="grid items-start gap-6 lg:grid-cols-[260px_minmax(0,1fr)]">
        <aside class="card p-4">
          <h2 class="mb-4 font-semibold">{{ t('controlledExperiments.history') }}</h2>
          <p v-if="!runs.length" class="py-6 text-sm text-gray-500">{{ loading ? t('controlledExperiments.loading') : t('controlledExperiments.empty') }}</p>
          <button v-for="run in runs" :key="run.id" class="mb-2 w-full rounded-lg border p-3 text-left text-sm" :class="selectedId === run.id ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/10' : 'border-gray-200 dark:border-dark-700'" @click="selectRun(run.id)">
            <strong class="block truncate">#{{ run.id }} · {{ run.name }}</strong><span class="mt-1 block text-xs text-gray-500">{{ stateLabel(run.status) }} · {{ run.reserved_calls }}/{{ run.max_calls }}</span><time class="mt-1 block text-xs text-gray-400">{{ date(run.created_at) }}</time>
          </button>
          <button v-if="hasMore" class="btn btn-secondary w-full" :disabled="loading" @click="loadMore">{{ t('controlledExperiments.more') }}</button>
        </aside>
        <section v-if="report" class="min-w-0 space-y-5" aria-live="polite">
          <div class="card p-5">
            <div class="flex flex-wrap items-center justify-between gap-3"><div><h2 class="font-semibold">#{{ report.run.id }} · {{ report.run.name }}</h2><p class="mt-1 text-sm text-gray-500">{{ stateLabel(report.run.status) }} · {{ t('controlledExperiments.budget', { used: report.run.reserved_calls, limit: report.run.max_calls }) }}</p></div><button class="btn btn-secondary" @click="exportReport">{{ t('controlledExperiments.export') }}</button></div>
            <p class="mt-4 break-words text-sm"><span class="text-gray-500">{{ t('controlledExperiments.configuration') }}</span> · <code>{{ report.run.spec.model }}</code> · {{ report.run.spec.reasoning_effort }} · {{ report.run.spec.suite_version }} · {{ t('controlledExperiments.repetitions') }} {{ report.run.spec.repetitions }}</p>
            <p v-if="report.run.stop_reason" class="mt-2 text-xs text-gray-500">{{ report.run.stop_reason }}</p>
            <div class="mt-4 flex flex-wrap gap-3">
              <button v-if="report.run.status === 'draft'" class="btn btn-primary" :disabled="busy" data-testid="experiment-start" @click="start">{{ t('controlledExperiments.start', { count: report.run.max_calls }) }}</button>
              <button v-if="report.run.status === 'running' || report.run.status === 'draft'" class="btn btn-secondary" :disabled="busy" @click="stop">{{ t('controlledExperiments.stop') }}</button>
            </div>
          </div>
          <div class="grid gap-4 xl:grid-cols-2">
            <article v-for="summary in report.routes" :key="summary.route_index" class="card p-5">
              <h3 class="font-semibold">{{ routeLabel(summary.route_index) }}</h3>
              <p class="mt-2 break-words text-xs text-gray-500" data-testid="experiment-frozen-route">{{ t('controlledExperiments.mappedModel') }}: {{ report.run.spec.routes[summary.route_index].mapped_model }} · {{ t('controlledExperiments.parentAccount') }}: {{ report.run.spec.routes[summary.route_index].parent_account_id ?? '—' }} · {{ t('controlledExperiments.proxy') }}: {{ report.run.spec.routes[summary.route_index].proxy_id ?? '—' }}</p>
              <p class="mt-1 text-sm text-gray-500">{{ t('controlledExperiments.eligibility') }} · {{ stateLabel(summary.eligibility) }}</p>
              <p v-if="preflightFor(summary.route_index)" class="mt-2 break-words text-xs text-gray-500">{{ t('controlledExperiments.preflight') }}: {{ preflightLabel(preflightFor(summary.route_index)?.reason || '') }} · {{ t('controlledExperiments.catalog') }}: {{ preflightFor(summary.route_index)?.catalog }}</p>
              <div v-if="preflightFor(summary.route_index)?.available === false" class="mt-3 rounded-lg bg-amber-50 p-3 text-sm leading-6 text-amber-800 dark:bg-amber-900/20 dark:text-amber-200" data-testid="experiment-route-blocked">
                <p class="font-medium">{{ t('controlledExperiments.routeNotExecuted') }}</p>
                <p v-if="preflightFor(summary.route_index)?.reason === 'websocket_not_enabled'">{{ t('controlledExperiments.wsPrerequisiteHint') }}</p>
              </div>
              <dl class="mt-4 grid grid-cols-2 gap-4 text-sm">
                <div><dt class="text-gray-500">{{ t('controlledExperiments.completed') }}</dt><dd class="mt-1 text-lg font-semibold">{{ summary.completed_calls }} / {{ summary.calls }}</dd></div>
                <div><dt class="text-gray-500">{{ t('controlledExperiments.score') }}</dt><dd class="mt-1 text-lg font-semibold" data-testid="experiment-score">{{ percentage(summary.mean_score) }}</dd></div>
                <div><dt class="text-gray-500">{{ t('controlledExperiments.protocol') }}</dt><dd>{{ summary.protocol_failures }}</dd></div>
                <div><dt class="text-gray-500">{{ t('controlledExperiments.unknown') }}</dt><dd>{{ summary.unknown_calls }}</dd></div>
                <div><dt class="text-gray-500">{{ t('controlledExperiments.passed') }}</dt><dd>{{ summary.passed_tasks }} / {{ summary.graded_tasks }}</dd></div>
                <div><dt class="text-gray-500">{{ t('controlledExperiments.cost') }}</dt><dd>${{ summary.cost_usd.toFixed(6) }} <span v-if="summary.cost_incomplete" class="text-xs text-amber-600">{{ t('controlledExperiments.incompleteCost') }}</span></dd></div>
              </dl>
              <p class="mt-4 text-xs text-gray-500">{{ t('controlledExperiments.coverage', { done: summary.graded_tasks, total: (report.run.spec.tasks?.length || 0) * report.run.spec.repetitions }) }}</p>
            </article>
          </div>
          <article v-if="report.comparisons.length" class="card p-5">
            <h3 class="font-semibold">{{ t('controlledExperiments.comparisons') }}</h3><p class="mb-4 mt-2 text-xs leading-5 text-gray-500">{{ t('controlledExperiments.comparisonHint') }}</p>
            <div class="overflow-x-auto"><table class="experiment-table"><thead><tr><th>{{ t('controlledExperiments.pair') }}</th><th>{{ t('controlledExperiments.paired') }}</th><th>{{ t('controlledExperiments.excluded') }}</th><th>{{ t('controlledExperiments.leftScore') }}</th><th>{{ t('controlledExperiments.rightScore') }}</th><th>{{ t('controlledExperiments.difference') }}</th></tr></thead><tbody><tr v-for="pair in report.comparisons" :key="`${pair.left}/${pair.right}`"><td>{{ pair.left + 1 }} ↔ {{ pair.right + 1 }}</td><td>{{ pair.paired_tasks }}</td><td>{{ pair.excluded_tasks }}</td><td>{{ percentage(pair.left_mean) }}</td><td>{{ percentage(pair.right_mean) }}</td><td>{{ percentage(pair.mean_difference) }}</td></tr></tbody></table></div>
          </article>
          <article class="card p-5">
            <h3 class="mb-4 font-semibold">{{ t('controlledExperiments.submissions') }}</h3>
            <div class="overflow-x-auto"><table class="experiment-table"><thead><tr><th>{{ t('controlledExperiments.sequence') }}</th><th>{{ t('controlledExperiments.task') }}</th><th>{{ t('controlledExperiments.actual') }}</th><th>{{ t('controlledExperiments.result') }}</th><th>{{ t('controlledExperiments.score') }}</th><th>{{ t('controlledExperiments.evidence') }}</th></tr></thead><tbody>
              <tr v-for="attempt in report.attempts" :key="attempt.sequence" data-testid="experiment-attempt">
                <td>{{ attempt.sequence }}</td><td><div>{{ attempt.phase === 'eligibility' ? t('controlledExperiments.eligibilityProbe') : attempt.task_id }}</div><div class="text-xs text-gray-400">{{ attempt.repetition }} / {{ attempt.turn }}</div></td>
                <td><div>{{ attempt.route_index + 1 }} · {{ attempt.diagnostic.actual_channel ? channelLabel(attempt.diagnostic.actual_channel) : '—' }}</div><code class="text-xs">{{ attempt.diagnostic.response_model || '—' }}</code></td>
                <td><span :class="attempt.status === 'completed' ? 'text-emerald-600' : 'text-amber-600'">{{ stateLabel(attempt.status) }}</span><div class="mt-1 text-xs text-gray-400">{{ t('controlledExperiments.duration', { seconds: (attempt.diagnostic.duration_ms / 1000).toFixed(1) }) }}</div></td>
                <td>{{ attempt.phase === 'task' && attempt.grade ? percentage(attempt.grade.score) : t('controlledExperiments.noScore') }}<div v-if="attempt.grade" class="mt-1 max-w-44 break-words text-xs text-gray-500">{{ attempt.grade.reason }}</div></td>
                <td><details><summary class="cursor-pointer text-xs">{{ attempt.diagnostic.code || attempt.status }}</summary><div class="min-w-60 space-y-3 py-3"><pre class="experiment-content max-h-80">{{ JSON.stringify(attempt.diagnostic, null, 2) }}</pre><details v-if="promptFor(attempt.task_id)"><summary>{{ t('controlledExperiments.prompt') }}</summary><pre class="experiment-content mt-2 max-h-60">{{ promptFor(attempt.task_id) }}</pre></details><strong class="block text-xs">{{ t('controlledExperiments.answer') }}</strong><pre class="experiment-content max-h-60">{{ attempt.answer || '—' }}</pre><template v-if="attempt.tool_trace?.length"><strong class="block text-xs">{{ t('controlledExperiments.localTools') }}</strong><pre class="experiment-content max-h-60">{{ JSON.stringify(attempt.tool_trace, null, 2) }}</pre></template></div></details></td>
              </tr>
            </tbody></table></div>
          </article>
        </section>
        <div v-else class="card p-8 text-sm text-gray-500">{{ t('controlledExperiments.selectRun') }}</div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import SmartOpsNav from '@/components/admin/operations/SmartOpsNav.vue'
import { list as listAccounts } from '@/api/admin/accounts'
import { controlledExperimentsAPI, type ExperimentChannel, type ExperimentInput, type ExperimentReport, type ExperimentRun, type ExperimentTask } from '@/api/admin/controlledExperiments'

const { t, te } = useI18n()
const channels: ExperimentChannel[] = ['native_http', 'native_ws', 'prism', 'bps']
const efforts = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra']
const accounts = ref<{ id: number; name: string }[]>([]), tasks = ref<ExperimentTask[]>([]), runs = ref<ExperimentRun[]>([])
const report = ref<ExperimentReport | null>(null), selectedId = ref<number | null>(null)
const loading = ref(false), busy = ref(false), error = ref(''), hasMore = ref(false)
const configurationReady = ref(false)
const form = reactive<ExperimentInput>({ name: t('controlledExperiments.newRun'), model: 'gpt-6.1-sol', reasoning_effort: 'high', split: 'screen', repetitions: 1, max_calls: 50, timeout_seconds: 720, task_ids: [], routes: [{ account_id: 0, channel: 'native_http' }] })
const splitTasks = computed(() => tasks.value.filter(task => task.split === form.split))
const selectedTasks = computed(() => splitTasks.value.filter(task => form.task_ids.includes(task.id)))
const plannedCalls = computed(() => form.routes.length * (1 + selectedTasks.value.reduce((sum, task) => sum + task.max_turns, 0) * form.repetitions))
const validForm = computed(() => configurationReady.value && selectedTasks.value.length > 0 && form.routes.every(route => route.account_id > 0) && form.max_calls >= 1 && form.max_calls <= 300 && form.repetitions >= 1 && form.repetitions <= 3)
const active = computed(() => report.value && ['running', 'stop_requested'].includes(report.value.run.status))
let timer: ReturnType<typeof setTimeout> | undefined, disposed = false

function selectAll() { form.task_ids = splitTasks.value.map(task => task.id) }
watch(() => form.split, selectAll)
function stateLabel(state: string) { const key = `controlledExperiments.state.${state}`; return te(key) ? t(key) : state }
function channelLabel(channel: string) { const key = `controlledExperiments.${channel}`; return te(key) ? t(key) : channel }
function preflightLabel(reason: string) { const key = `controlledExperiments.preflightReason.${reason}`; return te(key) ? t(key) : reason }
function percentage(value: number | null | undefined) { return value == null ? '—' : `${(value * 100).toFixed(1)}%` }
function date(value: string) { return new Date(value).toLocaleString() }
function routeLabel(index: number) { const route = report.value?.run.spec.routes[index]; return route ? `${index + 1} · #${route.account_id} ${route.account_name} · ${channelLabel(route.channel)}` : String(index + 1) }
function preflightFor(index: number) { return report.value?.preflight.find(item => item.route_index === index) }
function promptFor(id: string) { return report.value?.run.spec.tasks?.find(task => task.id === id)?.prompt }
function showError(err: unknown) {
  const e = err as { message?: string; response?: { data?: { message?: string } } }
  error.value = e.response?.data?.message || e.message || t('controlledExperiments.failure')
}
function schedulePoll() {
  clearTimeout(timer)
  if (!disposed && active.value) timer = setTimeout(async () => {
    try { await loadReport() } catch (err) { showError(err) } finally { schedulePoll() }
  }, 3000)
}
async function loadReport() {
  const id = selectedId.value
  if (!id) return
  const data = await controlledExperimentsAPI.report(id)
  if (disposed || selectedId.value !== id) return
  report.value = data
  const index = runs.value.findIndex(run => run.id === id)
  if (index >= 0) runs.value[index] = data.run
}
async function selectRun(id: number) {
  clearTimeout(timer); selectedId.value = id; report.value = null; error.value = ''
  try { await loadReport() } catch (err) { showError(err) } finally { schedulePoll() }
}
async function refresh() {
  if (loading.value) return
  loading.value = true; error.value = ''
  try {
    await loadConfiguration()
    const data = await controlledExperimentsAPI.list()
    runs.value = data; hasMore.value = data.length === 30
    if (selectedId.value) await loadReport()
    else if (data.length) await selectRun(data[0].id)
  } catch (err) { showError(err) } finally { loading.value = false; schedulePoll() }
}
async function loadMore() {
  loading.value = true
  try { const data = await controlledExperimentsAPI.list(runs.value.at(-1)?.id); runs.value.push(...data); hasMore.value = data.length === 30 } catch (err) { showError(err) } finally { loading.value = false }
}
async function create() {
  if (!validForm.value || busy.value) return
  busy.value = true; error.value = ''
  try {
    const run = await controlledExperimentsAPI.create({ ...form, task_ids: [...form.task_ids], routes: form.routes.map(route => ({ ...route })) })
    runs.value.unshift(run); await selectRun(run.id)
  } catch (err) { showError(err) } finally { busy.value = false }
}
async function start() {
  if (!report.value || report.value.run.status !== 'draft' || busy.value) return
  busy.value = true; error.value = ''
  try { await controlledExperimentsAPI.start(report.value.run.id); await loadReport() } catch (err) { showError(err) } finally { busy.value = false; schedulePoll() }
}
async function stop() {
  if (!report.value || busy.value) return
  busy.value = true
  try { await controlledExperimentsAPI.stop(report.value.run.id); await loadReport() } catch (err) { showError(err) } finally { busy.value = false; schedulePoll() }
}
function exportReport() {
  if (!report.value) return
  const url = URL.createObjectURL(new Blob([JSON.stringify(report.value, null, 2)], { type: 'application/json' }))
  const link = document.createElement('a'); link.href = url; link.download = `controlled-experiment-${report.value.run.id}.json`; link.click(); URL.revokeObjectURL(url)
}
async function loadConfiguration() {
  if (configurationReady.value) return
  const results = await Promise.allSettled([controlledExperimentsAPI.catalog(), listAccounts(1, 100, { platform: 'openai', lite: 'true' })])
  if (results[0].status === 'fulfilled' && !tasks.value.length) { tasks.value = results[0].value.tasks; selectAll() }
  if (results[1].status === 'fulfilled') {
    accounts.value = results[1].value.items.map(({ id, name }) => ({ id, name }))
    for (let page = 2; accounts.value.length < results[1].value.total; page++) {
      const data = await listAccounts(page, 100, { platform: 'openai', lite: 'true' })
      if (!data.items.length) throw new Error(t('controlledExperiments.incompleteAccounts'))
      accounts.value.push(...data.items.map(({ id, name }) => ({ id, name })))
    }
  }
  if (results[0].status === 'rejected') throw results[0].reason
  if (results[1].status === 'rejected') throw results[1].reason
  configurationReady.value = true
}
onMounted(refresh)
onUnmounted(() => { disposed = true; clearTimeout(timer) })
</script>

<style scoped>
.experiment-field { @apply flex flex-col gap-2 text-sm text-gray-600 dark:text-gray-300; }
.experiment-content { @apply overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-xs leading-5 text-gray-600 dark:bg-dark-800 dark:text-gray-300; overflow-wrap: anywhere; }
.experiment-table { @apply w-full text-left text-sm; }
.experiment-table th { @apply whitespace-nowrap border-b border-gray-200 pb-3 pr-4 text-xs font-medium text-gray-500 dark:border-dark-700; }
.experiment-table td { @apply border-b border-gray-100 py-3 pr-4 align-top dark:border-dark-800; }
</style>
