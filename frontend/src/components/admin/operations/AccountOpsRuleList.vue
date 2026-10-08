<template>
  <section
    class="rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900"
    data-testid="account-ops-rules"
  >
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
      <div>
        <h3 class="text-sm font-semibold">{{ t('accountOps.accountRules') }}</h3>
        <p class="mt-1 text-xs text-gray-500">{{ t('accountOps.rulesOnceSummary') }}</p>
      </div>
      <div class="flex flex-wrap gap-2">
        <input
          v-model="search"
          class="input w-48 text-sm"
          :placeholder="t('accountOps.search')"
          :aria-label="t('accountOps.search')"
        />
        <select v-model="type" class="input w-auto text-sm" :aria-label="t('accountOps.accountType')">
          <option value="all">{{ t('accountOps.allAccounts') }}</option>
          <option value="apikey">API Key</option>
          <option value="oauth">OAuth</option>
        </select>
        <select v-model="state" class="input w-auto text-sm" :aria-label="t('accountOps.ruleState')">
          <option value="all">{{ t('accountOps.allStates') }}</option>
          <option v-for="value in states" :key="value" :value="value">
            {{ t(`accountOps.ruleStates.${value}`) }}
          </option>
        </select>
      </div>
    </div>
    <slot name="global-settings" />
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-3 dark:border-dark-700">
      <div class="min-w-0 space-y-1">
        <p class="text-sm font-medium" aria-live="polite">
          {{ t('accountOps.selectedAccounts', { count: selectedAccounts.length }) }}
          <span v-if="selectedAccounts.length" class="ml-2 text-xs text-gray-500">
            {{ selectedTypes.join(' / ') }}
          </span>
        </p>
        <p class="text-xs leading-5 text-gray-500">{{ t('accountOps.batchSelectionHint') }}</p>
      </div>
      <div class="flex shrink-0 items-center gap-3">
        <button
          v-if="selectedAccounts.length"
          type="button"
          class="text-sm text-gray-500 hover:underline"
          :disabled="busy"
          @click="clearSelection"
        >
          {{ t('accountOps.clearSelection') }}
        </button>
        <button
          type="button"
          class="btn btn-secondary text-sm"
          data-testid="batch-edit-rules"
          :disabled="selectionUnavailable || !selectedAccounts.length"
          @click="editBatch"
        >
          {{ t('accountOps.batchEditRules') }}
        </button>
      </div>
    </div>
    <div class="overflow-x-auto">
      <table class="w-full min-w-[780px] text-left text-sm">
        <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800">
          <tr>
            <th class="w-12 py-3 pl-5 pr-2">
              <input
                type="checkbox"
                data-testid="select-visible-accounts"
                :aria-label="t('accountOps.selectVisibleAccounts')"
                :checked="allVisibleSelected"
                :indeterminate="selectedAccounts.length > 0 && !allVisibleSelected"
                :disabled="selectionUnavailable || !filtered.length"
                @change="selectVisible(($event.target as HTMLInputElement).checked)"
              />
            </th>
            <th class="px-3 py-3">{{ t('qualityOps.accounts') }}</th>
            <th class="px-4 py-3">{{ t('accountOps.metric') }}</th>
            <th class="px-4 py-3">{{ t('accountOps.currentValue') }}</th>
            <th class="px-4 py-3">{{ t('accountOps.ruleSummary') }}</th>
            <th class="px-5 py-3 text-right">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in filtered"
            :key="row.account.account_id"
            class="border-t border-gray-100 dark:border-dark-700"
            :data-testid="`rule-row-${row.account.account_id}`"
          >
            <td class="py-4 pl-5 pr-2">
              <input
                type="checkbox"
                :data-testid="`select-account-${row.account.account_id}`"
                :aria-label="t('accountOps.selectAccount', { name: row.account.account_name })"
                :checked="selectedIds.has(row.account.account_id)"
                :disabled="selectionUnavailable"
                @change="selectAccount(row.account, ($event.target as HTMLInputElement).checked)"
              />
            </td>
            <td class="px-3 py-4">
              <p class="font-medium text-gray-900 dark:text-gray-100">{{ row.account.account_name }}</p>
              <p class="mt-1 text-xs text-gray-500">
                {{ row.account.platform }} · {{ row.account.type === 'apikey' ? 'API Key' : 'OAuth' }}
              </p>
            </td>
            <td class="px-4 py-4 text-xs text-gray-500">
              {{ t(row.account.type === 'apikey' ? 'accountOps.balanceMetric' : 'accountOps.quotaMetric') }}
              <span v-if="row.windowLabel" class="mt-1 block">{{ row.windowLabel }}</span>
            </td>
            <td class="px-4 py-4">
              <p class="font-mono" :title="row.account.received_at || ''">{{ row.value }}</p>
              <span
                class="mt-1 inline-block text-xs"
                :class="row.state === 'warning' ? 'text-amber-700 dark:text-amber-400' : row.state === 'normal' ? 'text-emerald-700 dark:text-emerald-400' : 'text-gray-500'"
              >
                {{ t(`accountOps.ruleStates.${row.state}`) }}
              </span>
            </td>
            <td class="px-4 py-4">
              <template v-if="row.rule">
                <p class="text-xs">
                  {{ row.account.type === 'apikey' ? `≤ ${row.threshold.toFixed(2)} ${'unit' in row.rule ? row.rule.unit : row.account.unit}` : `≥ ${row.threshold}%` }}
                </p>
                <p class="mt-1 text-xs text-gray-500">{{ policy(row.rule.notify_alert, row.rule.notify_recovery) }}</p>
              </template>
              <span v-else class="text-xs text-gray-400">{{ t('accountOps.ruleStates.unconfigured') }}</span>
            </td>
            <td class="px-5 py-4">
              <div class="flex items-center justify-end gap-4">
                <label v-if="row.rule" class="flex items-center gap-1.5 text-xs text-gray-500">
                  <input
                    type="checkbox"
                    :checked="row.rule.enabled"
                    :disabled="busy"
                    :aria-label="`${row.account.account_name} ${t('accountOps.ruleEnabled')}`"
                    @change="emit('toggle', row.account, ($event.target as HTMLInputElement).checked)"
                  />
                  {{ t('accountOps.channelEnabled') }}
                </label>
                <button
                  type="button"
                  class="text-sm text-primary-600 hover:underline"
                  :data-testid="`edit-rule-${row.account.account_id}`"
                  @click="emit('edit', row.account)"
                >
                  {{ t('common.edit') }}
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-if="loading" role="status" class="p-8 text-center text-sm text-gray-500">{{ t('common.loading') }}</p>
    <p v-else-if="!filtered.length && !error" class="p-8 text-center text-sm text-gray-500">
      {{ t('accountOps.noThresholdAccounts') }}
    </p>
    <div
      v-for="rule in orphaned"
      :key="`${rule.metric}-${rule.account_id}`"
      class="flex items-center justify-between gap-3 border-t border-gray-100 px-5 py-3 text-xs text-amber-700 dark:border-dark-700"
    >
      <span>{{ t('accountOps.orphanRule', { id: rule.account_id }) }}</span>
      <button type="button" class="underline" @click="emit('remove', rule.account_id, rule.metric)">
        {{ t('common.delete') }}
      </button>
    </div>
  </section>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountOpsConfig, AccountOpsThresholdAccount } from '@/api/admin/accountOps'
const props = defineProps<{ accounts: AccountOpsThresholdAccount[]; config: AccountOpsConfig; loading: boolean; ready: boolean; error: string; busy?: boolean }>()
const emit = defineEmits<{
  (event: 'edit', account: AccountOpsThresholdAccount): void
  (event: 'batch-edit', accounts: AccountOpsThresholdAccount[]): void
  (event: 'toggle', account: AccountOpsThresholdAccount, enabled: boolean): void
  (event: 'remove', id: number, metric: 'balance' | 'quota'): void
}>()
const { t } = useI18n(), search = ref(''), type = ref('all'), state = ref('all')
const states = ['warning', 'normal', 'unknown', 'unconfigured', 'disabled']
const policy = (alert?: boolean, recovery?: boolean) => t((alert ?? true) ? (recovery ?? true) ? 'accountOps.policyBoth' : 'accountOps.policyAlert' : (recovery ?? true) ? 'accountOps.policyRecovery' : 'accountOps.policyRecord')
const rows = computed(() => props.accounts.map(account => {
  const rule = account.type === 'apikey' ? props.config.balance_thresholds?.find(r => r.account_id === account.account_id) : props.config.quota_thresholds?.find(r => r.account_id === account.account_id)
  const threshold = rule ? 'threshold' in rule ? rule.threshold : rule.threshold_percent : 0
  const selected = rule && 'window' in rule ? rule.window : 'any'
  const windows = account.usage_windows.filter(w => selected === 'any' || selected === '' || selected === w.window)
  const valid = windows.filter(w => w.status === 'ok' && w.used_percent != null && Number.isFinite(w.used_percent)).sort((a,b) => b.used_percent! - a.used_percent!)
  const value = account.type === 'apikey' ? account.balance_status === 'ok' && account.balance != null ? `${account.balance.toFixed(2)} ${account.unit}` : t(`accountOps.observations.${account.balance_status}`) : valid[0] ? `${valid[0].used_percent!.toFixed(1)}%` : t('accountOps.observations.unknown')
  const known = account.type === 'apikey' ? account.balance_status === 'ok' && account.balance != null && (!rule || !('unit' in rule) || rule.unit === account.unit) : windows.length > 0 && valid.length === windows.length
  const warning = account.type === 'apikey' ? known && account.balance! <= threshold : !!valid[0] && valid[0].used_percent! >= threshold
  return { account, rule, threshold, value, windowLabel: account.type === 'oauth' ? valid[0]?.label || (selected === 'any' ? t('accountOps.anyWindow') : selected) : '', state: !rule ? 'unconfigured' : !rule.enabled ? 'disabled' : warning ? 'warning' : known ? 'normal' : 'unknown' }
}))
const filtered = computed(() => rows.value.filter(r => (type.value === 'all' || type.value === r.account.type) && (state.value === 'all' || state.value === r.state) && `${r.account.account_name} ${r.account.account_id}`.toLowerCase().includes(search.value.toLowerCase().trim())))
const selectedIds = ref(new Set<number>())
const selectedAccounts = computed(() => filtered.value
  .filter(row => selectedIds.value.has(row.account.account_id))
  .map(row => row.account))
const selectionUnavailable = computed(() => props.busy || props.loading || !props.ready || !!props.error)
const selectedTypes = computed(() => [
  ...(selectedAccounts.value.some(account => account.type === 'apikey') ? ['API Key'] : []),
  ...(selectedAccounts.value.some(account => account.type === 'oauth') ? ['OAuth'] : [])
])
const allVisibleSelected = computed(() => filtered.value.length > 0
  && filtered.value.every(row => selectedIds.value.has(row.account.account_id)))

function clearSelection() {
  selectedIds.value = new Set()
}

watch(type, clearSelection, { flush: 'sync' })
watch(filtered, visible => {
  selectedIds.value = new Set(visible
    .filter(row => selectedIds.value.has(row.account.account_id))
    .map(row => row.account.account_id))
}, { flush: 'sync' })

function selectAccount(account: AccountOpsThresholdAccount, checked: boolean) {
  if (selectionUnavailable.value) return
  if (!filtered.value.some(row => row.account.account_id === account.account_id)) return
  const next = new Set(selectedIds.value)
  if (checked) next.add(account.account_id)
  else next.delete(account.account_id)
  selectedIds.value = next
}

function selectVisible(checked: boolean) {
  if (selectionUnavailable.value) return
  selectedIds.value = new Set(checked ? filtered.value.map(row => row.account.account_id) : [])
}

function editBatch() {
  if (!selectionUnavailable.value && selectedAccounts.value.length) emit('batch-edit', selectedAccounts.value)
}

defineExpose({ clearSelection })

const orphaned = computed(() => !props.ready || props.loading || props.error ? [] : [
  ...(props.config.balance_thresholds ?? []).filter(r => !props.accounts.some(a => a.account_id === r.account_id && a.type === 'apikey')).map(r => ({ ...r, metric: 'balance' as const })),
  ...(props.config.quota_thresholds ?? []).filter(r => !props.accounts.some(a => a.account_id === r.account_id && a.type === 'oauth')).map(r => ({ ...r, metric: 'quota' as const }))
])
</script>
