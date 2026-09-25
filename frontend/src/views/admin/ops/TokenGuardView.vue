<template>
  <AppLayout>
    <div class="token-guard">
      <SmartOpsNav />
      <header class="ops-heading">
        <div>
          <p class="eyebrow">{{ t('accountOps.smartTitle') }}</p>
          <h2>{{ t('tokenGuard.title') }}</h2>
          <p class="subtitle">{{ t('tokenGuard.description') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="load()">
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />{{ t('tokenGuard.refresh') }}
          </button>
          <button class="btn btn-primary inline-flex items-center gap-2" :disabled="running || loading" @click="run">
            <Icon name="play" size="sm" />{{ t(running ? 'tokenGuard.running' : 'tokenGuard.runNow') }}
          </button>
        </div>
      </header>

      <p v-if="error" role="alert" class="error-banner">{{ error }}</p>
      <p v-if="notice" role="status" class="success-banner">{{ notice }}</p>

      <section class="summary-grid">
        <article class="summary-card"><span>{{ t('tokenGuard.statsProbed') }}</span><strong>{{ remote?.runtime.stats.probed ?? 0 }}</strong><small>{{ t('tokenGuard.interval') }} {{ draft?.interval_seconds ?? 0 }}s</small></article>
        <article class="summary-card"><span>{{ t('tokenGuard.statsBad') }}</span><strong>{{ badCount }}</strong><small>{{ t('tokenGuard.failStreak') }} ≥ {{ draft?.fail_streak_threshold ?? 1 }}</small></article>
        <article class="summary-card"><span>{{ t('tokenGuard.statsRepaired') }}</span><strong>{{ remote?.runtime.stats.repaired ?? 0 }}</strong><small>{{ t('tokenGuard.stateFixed') }} {{ remote?.runtime.stats.state_fixed ?? 0 }}</small></article>
        <article class="summary-card"><span>{{ t('tokenGuard.lastRun') }}</span><strong class="text-base">{{ remote?.runtime.last_run ? date(remote.runtime.last_run) : t('tokenGuard.never') }}</strong><small>{{ remote?.runtime.last_message || '-' }}</small></article>
      </section>

      <div class="ops-columns">
        <section class="settings-card">
          <div class="section-title"><span class="icon-tile"><Icon name="shield" size="md" /></span><div><h3>{{ t('tokenGuard.title') }}</h3><p>{{ t('tokenGuard.enabledHint') }}</p></div></div>
          <form v-if="draft" class="settings-form" @submit.prevent="save">
            <fieldset :disabled="saving">
              <label class="enable-row"><span><strong>{{ t('tokenGuard.enabled') }}</strong><small>{{ t('tokenGuard.enabledHint') }}</small></span><input v-model="draft.enabled" type="checkbox" role="switch" :aria-label="t('tokenGuard.enabled')" /></label>
              <label class="field-label">{{ t('tokenGuard.groupIds') }}</label>
              <input v-model="groupIdsText" class="input w-full" placeholder="1, 2" />
              <p class="field-hint">{{ t('tokenGuard.groupIdsHint') }}</p>

              <div class="grid-2">
                <label class="field-label">{{ t('tokenGuard.interval') }}<input v-model.number="draft.interval_seconds" type="number" min="30" max="86400" class="input w-full" /></label>
                <label class="field-label">{{ t('tokenGuard.probeConcurrency') }}<input v-model.number="draft.probe_concurrency" type="number" min="1" max="16" class="input w-full" /></label>
                <label class="field-label">{{ t('tokenGuard.probeTimeout') }}<input v-model.number="draft.probe_timeout_seconds" type="number" min="5" max="900" class="input w-full" /></label>
                <label class="field-label">{{ t('tokenGuard.maxProbePerCycle') }}<input v-model.number="draft.max_probe_per_cycle" type="number" min="1" max="100" class="input w-full" /></label>
                <label class="field-label">{{ t('tokenGuard.failStreak') }}<input v-model.number="draft.fail_streak_threshold" type="number" min="1" max="10" class="input w-full" /></label>
              </div>

              <label class="field-label">{{ t('tokenGuard.probeEndpoint') }}<input v-model.trim="draft.probe_endpoint" class="input w-full" /></label>
              <label class="field-label">{{ t('tokenGuard.probeModel') }}<input v-model.trim="draft.probe_model" class="input w-full" placeholder="gpt-6-astra" /></label>
              <p class="field-hint">{{ t('tokenGuard.probeModelHint') }}</p>
              <label class="field-label">{{ t('tokenGuard.probeHeaders') }}</label>
              <textarea v-model="probeHeadersText" rows="3" class="input w-full" placeholder="Header-Name: value"></textarea>
              <p class="field-hint">{{ t('tokenGuard.headersHint') }}</p>

              <label class="kind-option"><input v-model="draft.auto_relogin" type="checkbox" /><span><strong>{{ t('tokenGuard.autoRelogin') }}</strong><small>{{ t('tokenGuard.reloginEndpoint') }}</small></span></label>
              <input v-model.trim="draft.relogin_endpoint" class="input w-full" />
              <label class="field-label">{{ t('tokenGuard.reloginHeaders') }}</label>
              <textarea v-model="reloginHeadersText" rows="3" class="input w-full" placeholder="Header-Name: value"></textarea>
              <p class="field-hint">{{ t('tokenGuard.headersHint') }}</p>
              <label class="kind-option"><input v-model="draft.restore_schedulable" type="checkbox" /><span><strong>{{ t('tokenGuard.restoreSchedulable') }}</strong><small>{{ t('tokenGuard.scopeNote') }}</small></span></label>

              <label class="field-label">{{ t('tokenGuard.reloginAccounts') }}</label>
              <textarea v-model="reloginText" rows="7" class="input w-full font-mono text-xs" placeholder="user@example.com,password,JBSWY3DPEHPK3PXP"></textarea>
              <p class="field-hint">{{ t('tokenGuard.reloginAccountsHint') }}</p>

              <div class="grid-2">
                <label class="field-label">{{ t('tokenGuard.barkKey') }}<input v-model.trim="draft.bark_key" class="input w-full" placeholder="留空则不推送" /></label>
                <div>
                  <label class="kind-option"><input v-model="draft.notify_on_fix" type="checkbox" /><span><strong>{{ t('tokenGuard.notifyOnFix') }}</strong><small>Bark</small></span></label>
                  <label class="kind-option"><input v-model="draft.notify_on_fail" type="checkbox" /><span><strong>{{ t('tokenGuard.notifyOnFail') }}</strong><small>Bark</small></span></label>
                </div>
              </div>

              <div class="settings-actions"><span>{{ dirty ? t('tokenGuard.unsaved') : t('tokenGuard.saved') }}</span><button class="btn btn-primary" :disabled="saving || !dirty">{{ t(saving ? 'qualityOps.saving' : 'tokenGuard.save') }}</button></div>
            </fieldset>
          </form>
        </section>

        <div class="stack">
          <section class="events-card">
            <header class="events-heading"><div><h3>{{ t('tokenGuard.accounts') }}<span>{{ accounts.length }}</span></h3><p>{{ remote?.runtime.last_message || t('tokenGuard.description') }}</p></div></header>
            <div class="events-scroll">
              <table>
                <thead><tr><th>{{ t('tokenGuard.account') }}</th><th>{{ t('tokenGuard.dbStatus') }}</th><th>{{ t('tokenGuard.schedulable') }}</th><th>{{ t('tokenGuard.probe') }}</th><th>{{ t('tokenGuard.latency') }}</th><th>{{ t('tokenGuard.lastFix') }}</th><th>{{ t('tokenGuard.actions') }}</th></tr></thead>
                <tbody>
                  <tr v-for="item in accounts" :key="item.account_id">
                    <td><strong :title="item.account_name">{{ item.account_name || '-' }}</strong><small>#{{ item.account_id }}</small></td>
                    <td><span class="failure-badge" :class="item.account_status === 'error' ? 'danger' : ''">{{ item.account_status || '-' }}</span></td>
                    <td>{{ t(item.schedulable ? 'tokenGuard.on' : 'tokenGuard.off') }}</td>
                    <td><span class="failure-badge" :class="probeClass(item.probe_state)">{{ t(`tokenGuard.probeStates.${item.probe_state || 'pending'}`) }}</span><small>{{ item.probe_detail }}</small></td>
                    <td>{{ item.latency_ms ? item.latency_ms + 'ms' : '-' }}</td>
                    <td>{{ item.last_fix_at ? `${item.last_fix_action} · ${date(item.last_fix_at)}` : '-' }}<small>{{ item.last_fix_result }}</small></td>
                    <td><button class="link-btn" :disabled="reloginBusy === item.account_id" @click="relogin(item)">{{ t(reloginBusy === item.account_id ? 'tokenGuard.reloginBusy' : 'tokenGuard.relogin') }}</button></td>
                  </tr>
                </tbody>
              </table>
              <div v-if="!accounts.length" class="empty-state"><Icon name="shield" size="xl" /><h4>{{ t('tokenGuard.noAccounts') }}</h4></div>
            </div>
          </section>

          <section class="events-card">
            <header class="events-heading"><div><h3>{{ t('tokenGuard.events') }}<span>{{ events.length }}</span></h3><p>{{ remote?.runtime.last_message || '-' }}</p></div></header>
            <div class="events-scroll short">
              <table>
                <thead><tr><th>时间</th><th>{{ t('tokenGuard.account') }}</th><th>类型</th><th>详情</th></tr></thead>
                <tbody>
                  <tr v-for="event in events" :key="event.id">
                    <td class="whitespace-nowrap">{{ date(event.created_at) }}</td>
                    <td>{{ event.account_name || '-' }}<small v-if="event.account_id">#{{ event.account_id }}</small></td>
                    <td><span class="failure-badge" :class="eventClass(event.kind)">{{ t(`tokenGuard.eventKinds.${event.kind}`) }}</span></td>
                    <td class="detail">{{ event.detail }}</td>
                  </tr>
                </tbody>
              </table>
              <div v-if="!events.length" class="empty-state"><Icon name="infoCircle" size="xl" /><h4>{{ t('tokenGuard.noEvents') }}</h4></div>
            </div>
          </section>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import SmartOpsNav from '@/components/admin/operations/SmartOpsNav.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  getTokenGuardStatus,
  reloginTokenGuardAccount,
  runTokenGuard,
  saveTokenGuardConfig,
  type TokenGuardConfig,
  type TokenGuardEvent,
  type TokenGuardStatus
} from '@/api/admin/accountTokenGuard'

const { t } = useI18n()
const remote = ref<TokenGuardStatus | null>(null)
const draft = ref<TokenGuardConfig | null>(null)
const groupIdsText = ref('')
const reloginText = ref('')
const probeHeadersText = ref('')
const reloginHeadersText = ref('')
const loading = ref(false), saving = ref(false), running = ref(false), reloginBusy = ref(0)
const error = ref(''), notice = ref('')
let timer: ReturnType<typeof setInterval> | undefined
let alive = true

const accounts = computed(() => remote.value?.accounts ?? [])
const events = computed<TokenGuardEvent[]>(() => remote.value?.events ?? [])
const badCount = computed(() => accounts.value.filter(item => item.probe_state === 'auth' || item.account_status === 'error').length)
const dirty = computed(() => {
  if (!draft.value || !remote.value) return false
  return JSON.stringify(collect()) !== JSON.stringify(normalize(remote.value.config))
})

const pad = (value: number) => String(value).padStart(2, '0')
const date = (value: string) => {
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? value : `${pad(parsed.getMonth() + 1)}-${pad(parsed.getDate())} ${pad(parsed.getHours())}:${pad(parsed.getMinutes())}`
}
const message = (e: unknown) => (e as { message?: string })?.message || t('qualityOps.error')
const parseGroupIds = (raw: string) => raw.split(/[,\s;]+/).map(value => Number(value.trim())).filter(value => Number.isFinite(value) && value > 0)
const parseRelogin = (raw: string) => raw.split(/\r?\n/).map(line => line.trim()).filter(Boolean).map(line => {
  const [email = '', password = '', mfa = ''] = line.split(',')
  return { email: email.trim(), password: password.trim(), mfa_secret: mfa.trim() }
}).filter(item => item.email && item.password)
const reloginTextOf = (config: TokenGuardConfig | null) => (config?.relogin_accounts ?? []).map(item => `${item.email},${item.password},${item.mfa_secret}`).join('\n')
const parseHeaders = (raw: string) => raw.split(/\r?\n/).reduce<Record<string, string>>((acc, line) => {
  const index = line.indexOf(':')
  if (index > 0) {
    const name = line.slice(0, index).trim()
    const value = line.slice(index + 1).trim()
    if (name && value) acc[name] = value
  }
  return acc
}, {})
const headersTextOf = (headers: Record<string, string> | undefined) => Object.entries(headers ?? {}).map(([name, value]) => `${name}: ${value}`).join('\n')
const groupTextOf = (config: TokenGuardConfig | null) => (config?.group_ids ?? []).join(', ')

function normalize(config: TokenGuardConfig): TokenGuardConfig {
  return {
    ...config,
    group_ids: [...(config.group_ids ?? [])].sort((a, b) => a - b),
    relogin_accounts: [...(config.relogin_accounts ?? [])].map(item => ({ email: item.email.toLowerCase(), password: item.password, mfa_secret: item.mfa_secret })).sort((a, b) => a.email.localeCompare(b.email)),
    probe_headers: config.probe_headers ?? {},
    relogin_headers: config.relogin_headers ?? {}
  }
}

function collect(): TokenGuardConfig {
  const base = draft.value!
  return normalize({ ...base, group_ids: parseGroupIds(groupIdsText.value), relogin_accounts: parseRelogin(reloginText.value),
    probe_headers: parseHeaders(probeHeadersText.value), relogin_headers: parseHeaders(reloginHeadersText.value) })
}

const probeClass = (state: string) => (state === 'ok' ? 'ok' : state === 'auth' ? 'danger' : '')
const eventClass = (kind: string) => (kind === 'relogin_ok' || kind === 'state_fixed' || kind === 'probe_ok' ? 'ok' : kind === 'relogin_failed' || kind === 'state_failed' || kind === 'probe_auth' ? 'danger' : '')

async function load(silent = false) {
  if (loading.value || saving.value) return
  loading.value = true
  if (!silent) error.value = ''
  try {
    const status = await getTokenGuardStatus()
    if (!alive) return
    const preserveDraft = dirty.value
    remote.value = status
    if (!preserveDraft) {
      draft.value = { ...status.config }
      groupIdsText.value = groupTextOf(status.config)
      reloginText.value = reloginTextOf(status.config)
      probeHeadersText.value = headersTextOf(status.config.probe_headers)
      reloginHeadersText.value = headersTextOf(status.config.relogin_headers)
    }
  } catch (e) {
    if (alive && !silent) error.value = message(e)
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!draft.value || saving.value) return
  saving.value = true; error.value = ''; notice.value = ''
  try {
    const saved = await saveTokenGuardConfig(collect())
    if (!alive) return
    draft.value = { ...saved }
    groupIdsText.value = groupTextOf(saved)
    reloginText.value = reloginTextOf(saved)
    probeHeadersText.value = headersTextOf(saved.probe_headers)
    reloginHeadersText.value = headersTextOf(saved.relogin_headers)
    if (remote.value) remote.value = { ...remote.value, config: saved }
    notice.value = t('tokenGuard.saved')
  } catch (e) {
    error.value = message(e)
  } finally {
    saving.value = false
  }
}

async function run() {
  if (running.value) return
  running.value = true; error.value = ''; notice.value = ''
  try {
    const stats = await runTokenGuard()
    if (!alive) return
    notice.value = t('tokenGuard.runDone', { probed: stats.probed, healthy: stats.healthy, repaired: stats.repaired, state_fixed: stats.state_fixed })
    await load(true)
  } catch (e) {
    error.value = message(e)
  } finally {
    running.value = false
  }
}

async function relogin(item: { account_id: number }) {
  if (reloginBusy.value) return
  reloginBusy.value = item.account_id; error.value = ''; notice.value = ''
  try {
    const result = await reloginTokenGuardAccount(item.account_id)
    if (!alive) return
    notice.value = t('tokenGuard.reloginDone', { action: result.action })
    await load(true)
  } catch (e) {
    error.value = message(e)
  } finally {
    reloginBusy.value = 0
  }
}

onMounted(() => {
  void load()
  timer = setInterval(() => { if (document.visibilityState === 'visible') void load(true) }, 30_000)
})
onBeforeUnmount(() => { alive = false; if (timer) clearInterval(timer) })
</script>

<style scoped>
.token-guard { max-width: 1660px; margin: auto; @apply text-gray-900 dark:text-gray-100; }
.ops-heading { @apply mb-6 flex flex-wrap items-center justify-between gap-4; }
.eyebrow { @apply mb-1 text-[11px] font-semibold tracking-widest text-primary-600; }
.ops-heading h2 { @apply text-2xl font-semibold tracking-tight; }
.subtitle { @apply mt-2 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400; }

.summary-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: 20px; @apply mb-5; }
.summary-card { @apply min-w-0 overflow-hidden rounded-2xl border border-gray-200/80 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-900; }
.summary-card > span { @apply block text-xs font-medium text-gray-400; }
.summary-card > strong { @apply mt-2 block text-2xl font-semibold tabular-nums tracking-tight; }
.summary-card > small { @apply mt-2 block truncate text-xs text-gray-400; }

.ops-columns { display: grid; grid-template-columns: minmax(320px,.32fr) minmax(0,.68fr); gap: 20px; }
.stack { display: flex; min-width: 0; flex-direction: column; gap: 20px; }
.settings-card, .events-card { @apply min-w-0 overflow-hidden rounded-2xl border border-gray-200/80 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900; }
.section-title { @apply flex items-center gap-3 border-b border-gray-100 p-5 dark:border-dark-700; }
.icon-tile { @apply flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-950/30; }
.section-title h3, .events-heading h3 { @apply text-base font-semibold; }
.section-title p, .events-heading p { @apply mt-1 text-xs text-gray-400; }
.settings-form { @apply p-5; }
.enable-row { @apply mb-6 flex items-center justify-between gap-4; }
.enable-row strong, .kind-option strong { @apply block text-sm font-medium; }
.enable-row small, .kind-option small { @apply mt-1 block text-xs leading-relaxed text-gray-400; }
.enable-row input { appearance:none; position:relative; width:40px; height:24px; border-radius:999px; cursor:pointer; flex-shrink:0; @apply bg-gray-200 transition-colors dark:bg-dark-600; }
.enable-row input::before { content:''; position:absolute; width:18px; height:18px; border-radius:50%; top:3px; left:3px; background:white; transition:transform .15s; box-shadow:0 1px 3px #0002; }
.enable-row input:checked { @apply bg-primary-600; }
.enable-row input:checked::before { transform:translateX(16px); }
.field-label { @apply mb-2 mt-5 block text-xs font-medium text-gray-600 dark:text-gray-300; }
.field-label .input { @apply mt-2; }
.field-hint { @apply mt-2 text-xs leading-relaxed text-gray-400; }
.kind-option { @apply mb-2 flex items-start gap-3 rounded-xl border border-gray-100 p-3 dark:border-dark-700; }
.kind-option:has(input:checked) { @apply border-primary-200 bg-primary-50/30 dark:border-primary-800 dark:bg-primary-950/20; }
.kind-option input { @apply mt-1 rounded text-primary-600; }
.settings-actions { @apply mt-5 flex items-center justify-between border-t border-gray-100 pt-4 dark:border-dark-700; }
.settings-actions span { @apply text-xs text-gray-400; }
.grid-2 { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 16px; }
.grid-2 .field-label { @apply mt-0; }

.events-card { @apply flex flex-col; }
.events-heading { @apply flex flex-wrap items-start justify-between gap-3 p-5; }
.events-heading h3 span { @apply ml-2 rounded-md bg-gray-100 px-2 py-0.5 text-xs font-normal tabular-nums text-gray-500 dark:bg-dark-800; }
.events-scroll { @apply min-h-0 flex-1 overflow-auto overscroll-contain; max-height: 24rem; scrollbar-gutter:stable; }
.events-scroll.short { max-height: 18rem; }
table { @apply w-full text-left text-xs; min-width: 780px; }
thead { @apply sticky top-0 z-10 bg-white text-gray-400 dark:bg-dark-900; }
th { @apply whitespace-nowrap px-4 py-3 font-medium; }
td { @apply border-b border-gray-100 px-4 py-3.5 align-top dark:border-dark-800; }
td strong { @apply block truncate text-[13px] font-medium; max-width: 220px; }
td small { @apply mt-1.5 block text-[10px] text-gray-400; }
tbody tr:hover { @apply bg-gray-50/70 dark:bg-dark-800/50; }
td.detail { max-width: 24rem; @apply whitespace-normal leading-5 text-gray-500 dark:text-gray-400; }
.failure-badge { @apply whitespace-nowrap rounded-md bg-amber-50 px-2 py-1 text-[11px] font-medium text-amber-700 dark:bg-amber-950/30 dark:text-amber-300; }
.failure-badge.ok { @apply bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300; }
.failure-badge.danger { @apply bg-red-50 text-red-600 dark:bg-red-950/30 dark:text-red-300; }
.link-btn { @apply rounded-lg border border-gray-200 px-2.5 py-1 text-[11px] font-medium text-primary-600 transition-colors hover:bg-primary-50 disabled:cursor-not-allowed disabled:opacity-40 dark:border-dark-600 dark:hover:bg-dark-800; }
.empty-state { @apply flex min-h-56 flex-col items-center justify-center gap-3 p-6 text-center text-gray-400; }
.empty-state h4 { @apply text-sm font-medium; }
.empty-state p { @apply max-w-sm text-xs leading-relaxed; }

.error-banner { @apply mb-4 rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300; }
.success-banner { @apply mb-4 rounded-xl bg-emerald-50 p-3 text-sm text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300; }
button:disabled { @apply cursor-not-allowed opacity-40; }
textarea.input { @apply font-mono text-[11px] leading-5; }
@media(max-width:1100px) { .ops-columns {grid-template-columns:1fr;} .events-scroll {max-height:28rem;} }
</style>
