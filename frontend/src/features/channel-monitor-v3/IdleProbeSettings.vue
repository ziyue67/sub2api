<template>
  <div class="card !rounded-2xl" data-testid="monitor-v3-idle-probe">
    <div class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
      <div class="min-w-0 max-w-3xl">
        <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV3.admin.idleProbe.title') }}</h3>
        <p class="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.admin.idleProbe.hint') }}</p>
      </div>
      <label class="flex shrink-0 items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-200">
        {{ t('channelMonitorV3.admin.idleProbe.enabled') }}
        <Toggle v-model="enabled" :aria-label="t('channelMonitorV3.admin.idleProbe.enabled')" data-testid="monitor-v3-idle-probe-enabled" />
      </label>
    </div>

    <div class="space-y-4 p-5">
      <p class="rounded-xl bg-amber-50 px-3 py-2 text-xs leading-relaxed text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
        {{ t('channelMonitorV3.admin.idleProbe.costHint') }} {{ t('channelMonitorV3.admin.idleProbe.modeHint') }}
      </p>

      <p v-if="!rows.length" class="rounded-xl border border-dashed border-gray-200 px-3 py-6 text-center text-xs text-gray-400 dark:border-dark-600" data-testid="monitor-v3-idle-probe-empty">
        {{ t('channelMonitorV3.admin.idleProbe.noGroups') }}
      </p>
      <ul v-else class="grid gap-3 lg:grid-cols-2">
        <li
          v-for="row in rows"
          :key="row.groupId"
          class="rounded-xl border px-4 py-3 transition"
          :class="drafts[row.groupId]?.checked ? 'border-primary-200 bg-primary-50/40 dark:border-primary-800/60 dark:bg-primary-900/10' : 'border-gray-100 dark:border-dark-700'"
          data-testid="monitor-v3-idle-probe-group"
          :data-group="row.groupId"
        >
          <label class="flex cursor-pointer items-start gap-2.5">
            <input
              v-model="drafts[row.groupId].checked"
              type="checkbox"
              class="mt-0.5 rounded text-primary-600"
              :disabled="row.unavailable && !drafts[row.groupId].checked"
              data-testid="monitor-v3-idle-probe-check"
            />
            <span class="min-w-0 flex-1">
              <span class="flex flex-wrap items-center gap-1.5 text-sm font-medium text-gray-900 dark:text-white">
                <span class="rounded px-1 py-px text-[10px]" :class="platformBadgeLightClass(row.platform)">{{ platformLabel(row.platform) }}</span>
                <span class="truncate">{{ row.name }}</span>
                <span class="text-xs font-normal text-gray-400">{{ t('channelMonitorV3.admin.idleProbe.componentCount', { count: row.components }) }}</span>
                <span v-if="row.unavailable" class="rounded-md bg-red-50 px-1.5 text-[10px] text-red-600 dark:bg-red-900/30 dark:text-red-300">{{ t('channelMonitorV3.admin.idleProbe.groupUnavailable') }}</span>
              </span>
            </span>
          </label>

          <div v-if="drafts[row.groupId].checked" class="mt-3 grid gap-3 sm:grid-cols-[minmax(0,1fr)_9rem]">
            <div class="min-w-0">
              <template v-if="row.wholeGroup">
                <label class="input-label" :for="`idle-probe-model-${row.groupId}`">{{ t('channelMonitorV3.admin.idleProbe.model') }}</label>
                <input
                  :id="`idle-probe-model-${row.groupId}`"
                  v-model="drafts[row.groupId].model"
                  class="input"
                  maxlength="100"
                  :placeholder="t('channelMonitorV3.admin.idleProbe.modelPlaceholder')"
                  :class="missingModel(row) ? '!border-red-300 dark:!border-red-700' : ''"
                  data-testid="monitor-v3-idle-probe-model"
                />
                <p class="mt-1 text-[11px]" :class="missingModel(row) ? 'text-red-500' : 'text-gray-400'">
                  {{ missingModel(row) ? t('channelMonitorV3.admin.idleProbe.modelRequired') : t('channelMonitorV3.admin.idleProbe.modelHint') }}
                </p>
              </template>
              <p v-else class="text-xs text-gray-500 dark:text-gray-400 sm:pt-1">
                {{ t('channelMonitorV3.admin.idleProbe.componentModels', { models: row.models.join('、') }) }}
              </p>
            </div>
            <div>
              <label class="input-label" :for="`idle-probe-effort-${row.groupId}`">{{ t('channelMonitorV3.admin.idleProbe.effort') }}</label>
              <select :id="`idle-probe-effort-${row.groupId}`" v-model="drafts[row.groupId].effort" class="input" :title="t('channelMonitorV3.admin.idleProbe.effortHint')" data-testid="monitor-v3-idle-probe-effort">
                <option v-for="effort in MONITOR_V3_PROBE_EFFORTS" :key="effort" :value="effort">{{ effort || t('channelMonitorV3.admin.idleProbe.effortDefault') }}</option>
              </select>
            </div>
          </div>

          <div class="mt-2 space-y-0.5 text-[11px] text-gray-500 dark:text-gray-400" data-testid="monitor-v3-idle-probe-stats">
            <template v-if="statOf(row.groupId)?.last_at">
              <p>{{ t('channelMonitorV3.admin.idleProbe.lastProbe', { time: formatMonitorV3Short(statOf(row.groupId)!.last_at!), result: lastResult(statOf(row.groupId)!) }) }}</p>
              <p>{{ t('channelMonitorV3.admin.idleProbe.last24h', { probes: statOf(row.groupId)!.probes, failures: statOf(row.groupId)!.failures, cost: formatCost(statOf(row.groupId)!.cost_usd) }) }}</p>
              <p v-if="configIssue(statOf(row.groupId)!)" class="text-amber-600 dark:text-amber-400" data-testid="monitor-v3-idle-probe-issue">
                {{ t('channelMonitorV3.admin.idleProbe.configIssue', { error: statOf(row.groupId)!.last_error }) }}
              </p>
            </template>
            <p v-else>{{ t('channelMonitorV3.admin.idleProbe.noProbes') }}</p>
          </div>
        </li>
      </ul>

      <div class="flex items-center justify-between border-t border-gray-100 pt-4 dark:border-dark-700">
        <span class="text-xs text-gray-400">{{ dirty ? t('channelMonitorV3.admin.unsaved') : t('channelMonitorV3.admin.saved') }}</span>
        <button type="button" class="btn btn-primary btn-sm" :disabled="!dirty || saving || invalid" data-testid="monitor-v3-idle-probe-save" @click="save">
          {{ t('common.save') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import {
  MONITOR_V3_PROBE_EFFORTS,
  updateIdleProbe,
  type MonitorV3Component,
  type MonitorV3IdleProbe,
  type MonitorV3IdleProbeStat,
} from '@/api/channelMonitorV3'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { platformBadgeLightClass, platformLabel } from '@/utils/platformColors'
import { formatMonitorV3Seconds, formatMonitorV3Short } from './monitorV3'

/** Failures that point at the probe settings; they leave the slot unjudged. */
const CONFIG_ISSUES = ['invalid_request', 'model_unsupported', 'not_found']
const DEFAULT_EFFORT = 'low'

const props = defineProps<{
  probe: MonitorV3IdleProbe
  stats: MonitorV3IdleProbeStat[]
  components: MonitorV3Component[]
}>()
const emit = defineEmits<{ (e: 'saved', probe: MonitorV3IdleProbe): void }>()
const { t, te } = useI18n()
const appStore = useAppStore()

type Row = { groupId: number; name: string; platform: string; components: number; wholeGroup: boolean; models: string[]; unavailable: boolean }
type Draft = { checked: boolean; model: string; effort: string }

/** One row per group the components use, in component order. */
const rows = computed<Row[]>(() => {
  const byGroup = new Map<number, Row>()
  for (const component of props.components) {
    let row = byGroup.get(component.group_id)
    if (!row) {
      row = {
        groupId: component.group_id, name: component.group_name || `#${component.group_id}`, platform: component.group_platform,
        components: 0, wholeGroup: false, models: [],
        unavailable: component.group_deleted || (!!component.group_status && component.group_status !== 'active'),
      }
      byGroup.set(component.group_id, row)
    }
    row.components++
    if (!component.model) row.wholeGroup = true
    else if (!row.models.includes(component.model)) row.models.push(component.model)
  }
  return [...byGroup.values()]
})

const enabled = ref(false)
const drafts = reactive<Record<number, Draft>>({})
const saving = ref(false)
const original = ref('')

function reset() {
  enabled.value = props.probe.enabled
  for (const key of Object.keys(drafts)) delete drafts[Number(key)]
  for (const row of rows.value) {
    const saved = props.probe.groups.find((group) => group.group_id === row.groupId)
    drafts[row.groupId] = { checked: !!saved, model: saved?.model || '', effort: saved ? saved.reasoning_effort : DEFAULT_EFFORT }
  }
  original.value = JSON.stringify(payload())
}

function payload(): MonitorV3IdleProbe {
  return {
    enabled: enabled.value,
    groups: rows.value
      .filter((row) => drafts[row.groupId]?.checked)
      .map((row) => ({ group_id: row.groupId, model: drafts[row.groupId].model.trim(), reasoning_effort: drafts[row.groupId].effort })),
  }
}

watch(() => [props.probe, props.components], reset, { immediate: true })

const dirty = computed(() => JSON.stringify(payload()) !== original.value)
const missingModel = (row: Row) => row.wholeGroup && !!drafts[row.groupId]?.checked && !drafts[row.groupId].model.trim()
const invalid = computed(() => rows.value.some(missingModel))

function statOf(groupId: number) {
  return props.stats.find((stat) => stat.group_id === groupId)
}

function reason(category: string | undefined) {
  const key = `channelMonitorV3.errors.${category || 'other'}`
  return te(key) ? t(key) : t('channelMonitorV3.errors.other')
}

function lastResult(stat: MonitorV3IdleProbeStat) {
  if (stat.last_outcome === 'success') {
    return t('channelMonitorV3.admin.idleProbe.lastSuccess', { seconds: formatMonitorV3Seconds(stat.last_latency_ms || 0) })
  }
  return t('channelMonitorV3.admin.idleProbe.lastFailure', { reason: reason(stat.last_category) })
}

function configIssue(stat: MonitorV3IdleProbeStat) {
  return stat.last_outcome === 'failure' && CONFIG_ISSUES.includes(stat.last_category || '') && !!stat.last_error
}

/** Probes cost fractions of a cent; keep enough digits to show them. */
function formatCost(value: number) {
  if (!value) return '0'
  return value < 0.01 ? value.toFixed(4) : value.toFixed(2)
}

async function save() {
  if (invalid.value) return
  saving.value = true
  try {
    const saved = await updateIdleProbe(payload())
    emit('saved', saved)
    appStore.showSuccess(t('channelMonitorV3.admin.idleProbe.saved'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('channelMonitorV3.admin.saveFailed')))
  } finally {
    saving.value = false
  }
}
</script>
