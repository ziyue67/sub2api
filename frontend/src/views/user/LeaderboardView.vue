
<template>
  <AppLayout>
    <div class="leaderboard-page space-y-6" :data-theme="theme">
     <!-- 顶部操作栏：时间范围 + 数量 + 刷新/重置 -->
      <section class="lb-card lb-toolbar">
        <div class="lb-toolbar-group">
          <span class="lb-toolbar-label">{{ t('leaderboard.periodLabel') }}</span>
          <div class="w-40">
            <Select v-model="days" :options="periodOptions" :theme="theme" @change="onDaysChange" />
          </div>
        </div>
        <div class="lb-toolbar-group lb-toolbar-actions">
          <span class="lb-toolbar-label">{{ t('leaderboard.limit') }}</span>
          <div class="w-24">
            <Select v-model="limit" :options="limitOptions" :theme="theme" @change="onLimitChange" />
          </div>
          <button
            type="button"
            class="lb-btn"
            :disabled="loading"
            @click="reload"
          >
            <span :class="{ 'lb-spin': loading }">⟳</span>
            {{ t('leaderboard.refresh') }}
          </button>
          <button type="button" class="lb-btn" @click="resetFilters">
            {{ t('leaderboard.reset') }}
          </button>
          <button
            type="button"
            class="lb-icon-btn"
            :title="themeToggleLabel"
            @click="toggleTheme"
          >
            {{ theme === 'dark' ? '☀️' : '🌙' }}
          </button>
        </div>
      </section>

      <!-- 筛选面板：模型和分组从当前用户的真实用量中加载。 -->
      <section class="lb-card lb-filters-card">
        <div class="lb-filter-grid">
          <div class="lb-filter-col">
            <label class="lb-filter-label">{{ t('leaderboard.model') }}</label>
            <div class="w-full">
              <Select v-model="filterModel" :options="modelOptions" :theme="theme" searchable @change="reload" />
              <p v-if="filterOptionsLoading" class="lb-filter-hint">{{ t('common.loading') }}</p>
            </div>
          </div>
          <div class="lb-filter-col">
            <label class="lb-filter-label">{{ t('leaderboard.requestType') }}</label>
            <div class="w-full">
              <Select v-model="filterRequestType" :options="requestTypeOptions" :theme="theme" @change="reload" />
            </div>
          </div>
          <div class="lb-filter-col">
            <label class="lb-filter-label">{{ t('leaderboard.billingType') }}</label>
            <div class="w-full">
              <Select v-model="filterBillingType" :options="billingTypeOptions" :theme="theme" @change="reload" />
            </div>
          </div>
          <div class="lb-filter-col">
            <label class="lb-filter-label">{{ t('leaderboard.billingMode') }}</label>
            <div class="w-full">
              <Select v-model="filterBillingMode" :options="billingModeOptions" :theme="theme" @change="reload" />
            </div>
          </div>
          <div class="lb-filter-col">
            <label class="lb-filter-label">{{ t('leaderboard.group') }}</label>
            <div class="w-full">
              <Select v-model="filterGroup" :options="groupOptions" :theme="theme" searchable @change="reload" />
            </div>
          </div>
        </div>
      </section>

      <!-- Loading -->
      <section v-if="loading && !leaderboard" class="lb-card lb-state">
        <LoadingSpinner />
      </section>

      <!-- Error -->
      <section v-else-if="error" class="lb-card lb-state">
        <h2 class="lb-state-title">{{ t('leaderboard.errorTitle') }}</h2>
        <p class="lb-state-desc">{{ t('leaderboard.errorDescription') }}</p>
        <button type="button" class="lb-retry" @click="reload">{{ t('leaderboard.retry') }}</button>
      </section>

      <!-- Empty -->
      <section v-else-if="!items.length" class="lb-card lb-state">
        <h2 class="lb-state-title">{{ t('leaderboard.emptyTitle') }}</h2>
        <p class="lb-state-desc">{{ t('leaderboard.emptyDescription') }}</p>
      </section>

      <!-- Leaderboard table -->
      <section v-else class="lb-card lb-board">
        <div class="lb-board-head">
          <span class="lb-board-top">{{ t('leaderboard.top', { count: leaderboard?.limit ?? limit }) }}</span>
          <span v-if="generatedAtLabel" class="lb-board-updated">
            {{ t('leaderboard.generatedAt', { time: generatedAtLabel }) }}
          </span>
        </div>

        <div class="lb-table-wrap">
          <table class="lb-table">
            <thead>
              <tr>
                <th class="lb-col-rank">{{ t('leaderboard.rank') }}</th>
                <th class="lb-col-user">{{ t('leaderboard.user') }}</th>
                <th class="lb-col-num">{{ t('leaderboard.totalTokens') }}</th>
                <th class="lb-col-num lb-hide-sm">{{ t('leaderboard.inputTokensShort') }}</th>
                <th class="lb-col-num lb-hide-sm">{{ t('leaderboard.outputTokensShort') }}</th>
                <th class="lb-col-num lb-hide-sm">{{ t('leaderboard.cacheTokensShort') }}</th>
                <th class="lb-col-num lb-hide-sm">{{ t('leaderboard.imageOutputShort') }}</th>
                <th class="lb-col-num">{{ t('leaderboard.requests') }}</th>
                <th class="lb-col-num lb-hide-sm">{{ t('leaderboard.cost') }}</th>
                <th class="lb-col-num lb-hide-sm">{{ t('leaderboard.actualCost') }}</th>
                <th class="lb-col-num lb-hide-sm">{{ t('leaderboard.accountCost') }}</th>
                <th class="lb-col-time lb-hide-sm">{{ t('leaderboard.lastActive') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="item in items"
                :key="item.rank"
                class="lb-row"
                :class="{ 'lb-row--me': item.is_me }"
              >
                <td class="lb-col-rank">
                  <span class="lb-rank" :class="medalClass(item.rank)">
                    <span v-if="medalEmoji(item.rank)" class="lb-medal">{{ medalEmoji(item.rank) }}</span>
                    <span v-else>{{ item.rank }}</span>
                  </span>
                </td>
                <td class="lb-col-user">
                  <span class="lb-user">{{ item.is_me ? t('leaderboard.me') : item.user }}</span>
                  <span v-if="item.is_me" class="lb-me-tag">{{ t('leaderboard.currentUser') }}</span>
                </td>
                <td class="lb-col-num lb-strong">{{ formatTokens(item.total_tokens) }}</td>
                <td class="lb-col-num lb-hide-sm">{{ formatTokens(item.input_tokens) }}</td>
                <td class="lb-col-num lb-hide-sm">{{ formatTokens(item.output_tokens) }}</td>
                <td class="lb-col-num lb-hide-sm">{{ formatTokens(item.cache_tokens) }}</td>
                <td class="lb-col-num lb-hide-sm">{{ formatTokens(item.image_output_tokens) }}</td>
                <td class="lb-col-num">{{ formatNumber(item.requests) }}</td>
                <td class="lb-col-num lb-hide-sm">{{ formatCost(item.cost) }}</td>
                <td class="lb-col-num lb-hide-sm lb-strong">{{ formatCost(item.actual_cost) }}</td>
                <td class="lb-col-num lb-hide-sm">{{ formatCost(item.account_cost) }}</td>
                <td class="lb-col-time lb-hide-sm">{{ item.last_active_at || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Select from '@/components/common/Select.vue'
import {
  usageAPI,
  type TokenLeaderboardResponse,
  type LeaderboardParams,
  type LeaderboardSortBy,
  type LeaderboardBillingMode
} from '@/api/usage'
import type { UsageRequestType, ModelStat, GroupStat } from '@/types'

const { t } = useI18n()

type DaysWindow = 1 | 3 | 7 | 14 | 30

const THEME_STORAGE_KEY = 'leaderboard.theme'

const leaderboard = ref<TokenLeaderboardResponse | null>(null)
const loading = ref(false)
const error = ref(false)
const days = ref<DaysWindow>(1)
const limit = ref(20)
const sortBy = ref<LeaderboardSortBy>('tokens')
const theme = ref<'light' | 'dark'>('light')

// 筛选面板
const filterModel = ref<string | null>(null)
const filterRequestType = ref<UsageRequestType | null>(null)
const filterBillingType = ref<number | null>(null)
const filterBillingMode = ref<LeaderboardBillingMode | null>(null)
const filterGroup = ref<number | null>(null)
const availableModels = ref<ModelStat[]>([])
const availableGroups = ref<GroupStat[]>([])
const filterOptionsLoading = ref(false)

let abortController: AbortController | null = null

const periodOptions = computed(() => [
  { value: 1, label: t('leaderboard.period.day1') },
  { value: 3, label: t('leaderboard.period.day3') },
  { value: 7, label: t('leaderboard.period.day7') },
  { value: 14, label: t('leaderboard.period.day14') },
  { value: 30, label: t('leaderboard.period.day30') }
])

const limitOptions = computed(() => [
  { value: 10, label: '10' },
  { value: 20, label: '20' },
  { value: 50, label: '50' },
  { value: 100, label: '100' }
])

const modelOptions = computed(() => [
  { value: null, label: t('leaderboard.filter.modelPlaceholder') },
  ...Array.from(new Set(availableModels.value.map((item) => item.model).filter(Boolean)))
    .sort((a, b) => a.localeCompare(b))
    .map((model) => ({ value: model, label: model }))
])

const requestTypeOptions = computed(() => [
  { value: null, label: t('leaderboard.filter.typePlaceholder') },
  { value: 'sync', label: t('leaderboard.request.sync') },
  { value: 'stream', label: t('leaderboard.request.stream') },
  { value: 'ws_v2', label: t('leaderboard.request.wsV2') },
  { value: 'cyber', label: t('leaderboard.request.cyber') },
  { value: 'live', label: t('leaderboard.request.live') }
])

const billingTypeOptions = computed(() => [
  { value: null, label: t('leaderboard.filter.billingTypePlaceholder') },
  { value: 0, label: t('leaderboard.billingTypeBalance') },
  { value: 1, label: t('leaderboard.billingTypeSubscription') }
])

const billingModeOptions = computed(() => [
  { value: null, label: t('leaderboard.filter.billingModePlaceholder') },
  { value: 'token', label: t('leaderboard.billing.token') },
  { value: 'per_request', label: t('leaderboard.billing.perRequest') },
  { value: 'image', label: t('leaderboard.billing.image') },
  { value: 'video', label: t('leaderboard.billing.video') }
])

const groupOptions = computed(() => [
  { value: null, label: t('leaderboard.filter.groupPlaceholder') },
  ...availableGroups.value
    .filter((item) => item.group_id > 0)
    .sort((a, b) => a.group_name.localeCompare(b.group_name))
    .map((item) => ({ value: item.group_id, label: item.group_name || `#${item.group_id}` }))
])

const items = computed(() => leaderboard.value?.items ?? [])

const themeToggleLabel = computed(() =>
  theme.value === 'dark' ? t('leaderboard.theme.light') : t('leaderboard.theme.dark')
)

const generatedAtLabel = computed(() => {
  const end = leaderboard.value?.end
  if (!end) return ''
  const d = new Date(end)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  })
})

const browserTimezone = (() => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
})()

function medalEmoji(rank: number): string {
  switch (rank) {
    case 1:
      return '🥇'
    case 2:
      return '🥈'
    case 3:
      return '🥉'
    default:
      return ''
  }
}

function medalClass(rank: number): string {
  switch (rank) {
    case 1:
      return 'lb-rank--gold'
    case 2:
      return 'lb-rank--silver'
    case 3:
      return 'lb-rank--bronze'
    default:
      return ''
  }
}

const numberFormatter = new Intl.NumberFormat()

function formatNumber(value: number): string {
  return numberFormatter.format(value ?? 0)
}

function formatTokens(value: number): string {
  const v = value ?? 0
  if (v >= 1_000_000_000) return `${(v / 1_000_000_000).toFixed(2)}B`
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(2)}M`
  if (v >= 1_000) return `${(v / 1_000).toFixed(2)}K`
  return numberFormatter.format(v)
}

function formatCost(value: number): string {
  return `$${(value ?? 0).toFixed(4)}`
}

function buildParams(): LeaderboardParams {
  const params: LeaderboardParams = {
    days: days.value,
    sort_by: sortBy.value,
    limit: limit.value
  }
  if (filterModel.value) params.model = filterModel.value
  if (filterRequestType.value) params.request_type = filterRequestType.value
  if (filterBillingType.value !== null) params.billing_type = filterBillingType.value
  if (filterBillingMode.value) params.billing_mode = filterBillingMode.value
  if (filterGroup.value !== null) params.group_id = filterGroup.value
  if (browserTimezone) params.timezone = browserTimezone
  return params
}

async function load() {
  loading.value = true
  error.value = false

  if (abortController) {
    abortController.abort()
  }
  abortController = new AbortController()

  try {
    const data = await usageAPI.getDashboardLeaderboard(buildParams(), {
      signal: abortController.signal
    })
    leaderboard.value = data
  } catch (err: unknown) {
    if (
      (err as { name?: string })?.name === 'CanceledError' ||
      (err as { code?: string })?.code === 'ERR_CANCELED'
    ) {
      return
    }
    console.error('Failed to load leaderboard:', err)
    error.value = true
  } finally {
    loading.value = false
  }
}

function leaderboardDateRange() {
  const now = new Date()
  const start = new Date(now)
  start.setDate(now.getDate() - (days.value - 1))
  const toDate = (value: Date) => {
    const year = value.getFullYear()
    const month = String(value.getMonth() + 1).padStart(2, '0')
    const day = String(value.getDate()).padStart(2, '0')
    return `${year}-${month}-${day}`
  }
  return { start_date: toDate(start), end_date: toDate(now) }
}

async function loadFilterOptions() {
  filterOptionsLoading.value = true
  try {
    const snapshot = await usageAPI.getDashboardSnapshotV2({
      ...leaderboardDateRange(),
      include_trend: false,
      include_model_stats: true,
      include_group_stats: true,
      timezone: browserTimezone || undefined
    })
    availableModels.value = snapshot.models || []
    availableGroups.value = snapshot.groups || []
  } catch (err) {
    console.error('Failed to load leaderboard filter options:', err)
    availableModels.value = []
    availableGroups.value = []
  } finally {
    filterOptionsLoading.value = false
  }
}

function reload() {
  void load()
}

function onDaysChange() {
  void loadFilterOptions()
  void load()
}

function onLimitChange() {
  void load()
}

function resetFilters() {
  days.value = 1
  limit.value = 20
  sortBy.value = 'tokens'
  filterModel.value = null
  filterRequestType.value = null
  filterBillingType.value = null
  filterBillingMode.value = null
  filterGroup.value = null
  void loadFilterOptions()
  void load()
}

function applyTheme(next: 'light' | 'dark') {
  theme.value = next
  try {
    localStorage.setItem(THEME_STORAGE_KEY, next)
  } catch {
    /* ignore storage errors */
  }
}

function toggleTheme() {
  applyTheme(theme.value === 'dark' ? 'light' : 'dark')
}

onMounted(() => {
  try {
    const saved = localStorage.getItem(THEME_STORAGE_KEY)
    if (saved === 'dark' || saved === 'light') {
      theme.value = saved
    }
  } catch {
    /* ignore storage errors */
  }
  void loadFilterOptions()
  void load()
})

onBeforeUnmount(() => {
  if (abortController) {
    abortController.abort()
    abortController = null
  }
})
</script>

<style scoped>
.leaderboard-page {
  --lb-bg: #ffffff;
  --lb-fg: #1f2937;
  --lb-muted: #6b7280;
  --lb-border: #e5e7eb;
  --lb-row-hover: #f9fafb;
  --lb-me-bg: #eff6ff;
  --lb-me-border: #3b82f6;
  --lb-accent: #2563eb;
  --lb-input-bg: #ffffff;
  --lb-input-border: #d1d5db;
}

.leaderboard-page[data-theme='dark'] {
  --lb-bg: #0f172a;
  --lb-fg: #e2e8f0;
  --lb-muted: #94a3b8;
  --lb-border: #1e293b;
  --lb-row-hover: #1e293b;
  --lb-me-bg: #1e3a5f;
  --lb-me-border: #3b82f6;
  --lb-accent: #60a5fa;
  --lb-input-bg: #151e32;
  --lb-input-border: #334155;
}

/* 深色只覆盖排行榜内容区；导航栏、侧栏和其他路由保持原样。 */
.leaderboard-page[data-theme='dark'] {
  min-height: calc(100dvh - 8rem);
  padding: 1.5rem;
  margin: -1.5rem;
  background: #020617;
}

.lb-card {
  background: var(--lb-bg);
  color: var(--lb-fg);
  border: 1px solid var(--lb-border);
  border-radius: 12px;
  padding: 1.25rem;
}

.lb-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}

.lb-toolbar-group {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  flex-wrap: wrap;
}

.lb-toolbar-actions {
  gap: 0.75rem;
}

.lb-toolbar-label {
  color: var(--lb-muted);
  font-size: 0.85rem;
  font-weight: 500;
}

.lb-filter {
  display: grid;
  gap: 0.25rem;
  color: var(--lb-muted);
  font-size: 0.75rem;
  font-weight: 600;
}

.lb-filter-label {
  color: var(--lb-muted);
  font-size: 0.75rem;
  font-weight: 600;
  margin-bottom: 0.25rem;
  display: block;
}

.lb-filter-hint {
  margin: 0.35rem 0 0;
  color: var(--lb-muted);
  font-size: 0.75rem;
}

.lb-input {
  width: 100%;
  border: 1px solid var(--lb-input-border);
  border-radius: 8px;
  background: var(--lb-input-bg);
  color: var(--lb-fg);
  padding: 0.45rem 0.75rem;
  font-size: 0.85rem;
  outline: none;
  transition: border-color 0.15s ease;
}

.lb-input:focus {
  border-color: var(--lb-accent);
}

.lb-filters-card {
  padding: 1.25rem;
}

.lb-filter-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 1rem;
}

@media (max-width: 1200px) {
  .lb-filter-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (max-width: 768px) {
  .lb-filter-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 480px) {
  .lb-filter-grid {
    grid-template-columns: 1fr;
  }
}

.lb-btn {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  border-radius: 8px;
  border: 1px solid var(--lb-border);
  background: var(--lb-bg);
  color: var(--lb-fg);
  padding: 0.45rem 0.9rem;
  font-size: 0.85rem;
  cursor: pointer;
  transition: all 0.15s ease;
}

.lb-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.lb-btn-secondary:hover {
  border-color: var(--lb-accent);
  color: var(--lb-accent);
}

.lb-icon-btn {
  width: 34px;
  height: 34px;
  border-radius: 8px;
  border: 1px solid var(--lb-border);
  background: var(--lb-bg);
  color: var(--lb-fg);
  cursor: pointer;
  font-size: 1rem;
  line-height: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.lb-spin {
  display: inline-block;
  animation: lb-spin 0.8s linear infinite;
}

@keyframes lb-spin {
  to {
    transform: rotate(360deg);
  }
}

.lb-state {
  text-align: center;
  padding: 3rem 1.25rem;
}

.lb-state-title {
  font-size: 1.05rem;
  font-weight: 600;
  margin: 0 0 0.35rem;
}

.lb-state-desc {
  color: var(--lb-muted);
  font-size: 0.9rem;
  margin: 0 0 1rem;
}

.lb-retry {
  border: 1px solid var(--lb-accent);
  color: var(--lb-accent);
  background: transparent;
  border-radius: 8px;
  padding: 0.4rem 1.1rem;
  cursor: pointer;
  font-size: 0.85rem;
}

.lb-board-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 0.75rem;
}

.lb-board-top {
  font-weight: 600;
}

.lb-board-updated {
  font-size: 0.8rem;
  color: var(--lb-muted);
}

.lb-table-wrap {
  overflow-x: auto;
}

.lb-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.88rem;
}

.lb-table th,
.lb-table td {
  padding: 0.6rem 0.75rem;
  text-align: left;
  border-bottom: 1px solid var(--lb-border);
  white-space: nowrap;
}

.lb-table th {
  color: var(--lb-muted);
  font-weight: 600;
  font-size: 0.78rem;
  text-transform: uppercase;
  letter-spacing: 0.02em;
}

.lb-col-num {
  text-align: right;
}

.lb-col-time {
  text-align: right;
}

.lb-row:hover {
  background: var(--lb-row-hover);
}

.lb-row--me {
  background: var(--lb-me-bg);
  box-shadow: inset 3px 0 0 var(--lb-me-border);
}

.lb-row--me:hover {
  background: var(--lb-me-bg);
}

.lb-rank {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 1.75rem;
  font-weight: 600;
}

.lb-medal {
  font-size: 1.1rem;
}

.lb-rank--gold,
.lb-rank--silver,
.lb-rank--bronze {
  font-weight: 700;
}

.lb-user {
  font-weight: 500;
}

.lb-me-tag {
  margin-left: 0.4rem;
  font-size: 0.7rem;
  color: var(--lb-accent);
  border: 1px solid var(--lb-accent);
  border-radius: 999px;
  padding: 0.05rem 0.4rem;
}

.lb-strong {
  font-weight: 700;
}

@media (max-width: 640px) {
  .lb-hide-sm {
    display: none;
  }

  .lb-toolbar {
    flex-direction: column;
    align-items: stretch;
  }

  .lb-toolbar-group {
    justify-content: space-between;
  }
}
</style>
