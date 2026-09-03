<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.proxyLanes.title')"
    width="extra-wide"
    @close="handleClose"
  >
    <div class="space-y-5">
      <div class="rounded-xl border border-primary-100 bg-primary-50/70 p-4 dark:border-primary-500/20 dark:bg-primary-500/10">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <p class="text-sm font-semibold text-primary-900 dark:text-primary-100">
              {{ account?.name || `#${accountId}` }}
            </p>
            <p class="mt-1 text-xs text-primary-700 dark:text-primary-300">
              {{ t('admin.accounts.proxyLanes.description') }}
            </p>
          </div>
          <div class="rounded-lg bg-white/80 px-3 py-2 shadow-sm dark:bg-dark-800/70">
            <label class="block text-[11px] font-medium uppercase tracking-wide text-gray-500 dark:text-dark-400">
              {{ t('admin.accounts.proxyLanes.aggregate') }}
            </label>
            <div class="mt-1 flex items-center gap-2">
              <span class="font-mono text-sm text-gray-500 dark:text-dark-400">{{ currentConcurrency }} /</span>
              <input v-model.number="aggregateConcurrency" type="number" min="0" step="1" class="w-20 rounded-lg border border-gray-200 bg-white px-2 py-1 text-right font-mono text-sm font-semibold text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500/20 dark:border-dark-600 dark:bg-dark-800 dark:text-white" :aria-label="t('admin.accounts.proxyLanes.aggregate')" />
              <button type="button" class="btn btn-secondary px-2 py-1 text-xs" :disabled="aggregateSaving || !aggregateDirty" @click="saveAggregate">
                {{ aggregateSaving ? t('common.saving') : t('common.save') }}
              </button>
            </div>
          </div>
        </div>
        <p class="mt-3 text-xs text-primary-700 dark:text-primary-300">
          {{ t('admin.accounts.proxyLanes.aggregateHint') }}
        </p>
      </div>

      <div v-if="loadError" class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300">
        {{ loadError }}
      </div>

      <div class="flex items-center justify-between gap-3">
        <div>
          <h4 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('admin.accounts.proxyLanes.laneCount', { count: lanes.length }) }}
          </h4>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('admin.accounts.proxyLanes.laneCountHint') }}</p>
        </div>
        <div class="flex items-center gap-2">
          <button type="button" class="btn btn-secondary px-2.5 py-1.5 text-xs" :disabled="loading" @click="loadLanes">
            <Icon name="refresh" size="xs" :class="loading ? 'animate-spin' : ''" />
            <span class="hidden sm:inline">{{ t('common.refresh') }}</span>
          </button>
          <button type="button" class="btn btn-primary px-2.5 py-1.5 text-xs" :disabled="editing" @click="startCreate">
            <Icon name="plus" size="xs" />
            {{ t('admin.accounts.proxyLanes.add') }}
          </button>
        </div>
      </div>
      <div
        v-if="aggregateConcurrency > 0 && configuredLaneTotal > aggregateConcurrency"
        class="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
      >
        {{ t('admin.accounts.proxyLanes.aggregateWarning', { total: configuredLaneTotal, aggregate: aggregateConcurrency }) }}
      </div>

      <div v-if="!loading && lanes.length === 0" class="rounded-xl border border-dashed border-gray-300 px-4 py-8 text-center dark:border-dark-600">
        <Icon name="server" size="lg" class="mx-auto text-gray-400 dark:text-dark-500" />
        <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.proxyLanes.empty') }}</p>
        <p class="mt-1 text-xs text-gray-400 dark:text-dark-500">{{ t('admin.accounts.proxyLanes.emptyHint') }}</p>
      </div>

      <div v-else class="space-y-3" data-testid="account-proxy-lanes-list">
        <div
          v-for="lane in lanes"
          :key="lane.id"
          class="rounded-xl border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800"
          :class="lane.status !== 'active' || !lane.schedulable ? 'opacity-75' : ''"
        >
          <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-2">
                <span class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ lane.name }}</span>
                <span class="rounded-full px-2 py-0.5 text-[11px] font-medium" :class="transportClass(lane.transport)">
                  {{ lane.transport === 'direct' ? t('admin.accounts.proxyLanes.direct') : t('admin.accounts.proxyLanes.proxy') }}
                </span>
                <span class="rounded-full px-2 py-0.5 text-[11px] font-medium" :class="statusClass(lane.status)">
                  {{ statusLabel(lane.status) }}
                </span>
                <span v-if="lane.schedulable" class="text-[11px] text-emerald-600 dark:text-emerald-400">{{ t('admin.accounts.proxyLanes.schedulable') }}</span>
                <span v-else class="text-[11px] text-gray-400 dark:text-dark-500">{{ t('admin.accounts.proxyLanes.notSchedulable') }}</span>
              </div>
              <p class="mt-1 truncate text-xs text-gray-500 dark:text-dark-400" :title="laneEndpoint(lane)">{{ laneEndpoint(lane) }}</p>
            </div>
            <div class="grid grid-cols-3 gap-3 text-center sm:flex sm:items-center sm:gap-5">
              <div>
                <div class="text-[11px] text-gray-500 dark:text-dark-400">{{ t('admin.accounts.proxyLanes.laneCap') }}</div>
                <div class="font-mono text-sm font-semibold text-gray-900 dark:text-white">{{ lane.current_concurrency ?? 0 }} / {{ lane.concurrency || t('admin.accounts.proxyLanes.unlimited') }}</div>
              </div>
              <div>
                <div class="text-[11px] text-gray-500 dark:text-dark-400">{{ t('admin.accounts.proxyLanes.weight') }}</div>
                <div class="font-mono text-sm text-gray-700 dark:text-gray-200">{{ lane.weight }}</div>
              </div>
              <div>
                <div class="text-[11px] text-gray-500 dark:text-dark-400">{{ t('admin.accounts.proxyLanes.priority') }}</div>
                <div class="font-mono text-sm text-gray-700 dark:text-gray-200">{{ lane.priority }}</div>
              </div>
            </div>
            <div class="flex shrink-0 items-center justify-end gap-2 border-t border-gray-100 pt-3 dark:border-dark-700 lg:border-t-0 lg:pt-0">
              <button type="button" class="btn btn-secondary px-2.5 py-1.5 text-xs" :disabled="editing" @click="startEdit(lane)">
                <Icon name="edit" size="xs" /> {{ t('common.edit') }}
              </button>
              <button type="button" class="btn btn-secondary px-2.5 py-1.5 text-xs text-red-600 hover:border-red-200 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-500/10" :disabled="busyLaneId === lane.id" @click="deleteLane(lane)">
                <Icon name="trash" size="xs" /> {{ t('common.delete') }}
              </button>
            </div>
          </div>
        </div>
      </div>

      <form v-if="editing" class="rounded-xl border border-primary-200 bg-primary-50/40 p-4 dark:border-primary-500/30 dark:bg-primary-500/5" data-testid="account-proxy-lane-form" @submit.prevent="saveLane">
        <div class="mb-4 flex items-center justify-between gap-3">
          <h4 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ editingLaneId == null ? t('admin.accounts.proxyLanes.add') : t('admin.accounts.proxyLanes.edit') }}
          </h4>
          <button type="button" class="text-xs text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-white" @click="cancelEdit">{{ t('common.cancel') }}</button>
        </div>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div class="sm:col-span-2 lg:col-span-2">
            <label class="input-label">{{ t('admin.accounts.proxyLanes.name') }} <span class="text-red-500">*</span></label>
            <input v-model.trim="form.name" class="input" required maxlength="100" :placeholder="t('admin.accounts.proxyLanes.namePlaceholder')" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.proxyLanes.transport') }}</label>
            <select v-model="form.transport" class="input" @change="handleTransportChange">
              <option value="proxy">{{ t('admin.accounts.proxyLanes.proxy') }}</option>
              <option value="direct">{{ t('admin.accounts.proxyLanes.direct') }}</option>
            </select>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.proxyLanes.status') }}</label>
            <select v-model="form.status" class="input">
              <option value="active">{{ t('admin.accounts.proxyLanes.statusActive') }}</option>
              <option value="paused">{{ t('admin.accounts.proxyLanes.statusPaused') }}</option>
              <option value="error">{{ t('admin.accounts.proxyLanes.statusError') }}</option>
              <option value="disabled">{{ t('admin.accounts.proxyLanes.statusDisabled') }}</option>
            </select>
          </div>
          <div v-if="form.transport === 'proxy'" class="sm:col-span-2 lg:col-span-2">
            <label class="input-label">{{ t('admin.accounts.proxyLanes.proxyEndpoint') }} <span class="text-red-500">*</span></label>
            <select v-model="form.proxy_id" class="input" required>
              <option :value="null">{{ t('admin.accounts.proxyLanes.selectProxy') }}</option>
              <option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id" :disabled="proxy.status !== 'active'">
                {{ proxy.name }} · {{ proxy.protocol }}://{{ proxy.host }}:{{ proxy.port }}{{ proxy.status !== 'active' ? ` (${proxy.status})` : '' }}
              </option>
            </select>
            <p v-if="proxies.length === 0" class="input-hint text-amber-600 dark:text-amber-400">{{ t('admin.accounts.proxyLanes.noProxies') }}</p>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.proxyLanes.laneCap') }}</label>
            <input v-model.number="form.concurrency" type="number" min="0" step="1" class="input" />
            <p class="input-hint">{{ t('admin.accounts.proxyLanes.concurrencyHint') }}</p>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.proxyLanes.weight') }}</label>
            <input v-model.number="form.weight" type="number" min="0" step="1" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.proxyLanes.priority') }}</label>
            <input v-model.number="form.priority" type="number" min="0" step="1" class="input" />
          </div>
          <label class="flex items-center gap-2 self-end pb-2 text-sm text-gray-700 dark:text-gray-200">
            <input v-model="form.schedulable" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
            {{ t('admin.accounts.proxyLanes.schedulable') }}
          </label>
        </div>
        <div v-if="formError" class="mt-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-500/10 dark:text-red-300">{{ formError }}</div>
        <div class="mt-4 flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" :disabled="saving" @click="cancelEdit">{{ t('common.cancel') }}</button>
          <button type="submit" class="btn btn-primary" :disabled="saving">
            <Icon v-if="saving" name="refresh" size="sm" class="animate-spin" />
            {{ saving ? t('common.submitting') : t('common.save') }}
          </button>
        </div>
      </form>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="handleClose">{{ t('common.close') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminAPI } from '@/api/admin'
import type { Account, Proxy } from '@/types'
import type {
  AccountProxyLane,
  AccountProxyLanePayload,
  AccountProxyLaneStatus,
  AccountProxyLaneTransport
} from '@/api/admin/proxyLanes'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'

const props = defineProps<{
  show: boolean
  account: Account | null
  proxies: Proxy[]
}>()

const emit = defineEmits<{ (event: 'close'): void; (event: 'updated'): void }>()
const { t } = useI18n()
const appStore = useAppStore()

const lanes = ref<AccountProxyLane[]>([])
const loading = ref(false)
const saving = ref(false)
const aggregateSaving = ref(false)
const busyLaneId = ref<number | null>(null)
const loadError = ref('')
const formError = ref('')
const editingLaneId = ref<number | null>(null)
const formOpen = ref(false)
const editing = computed(() => formOpen.value)
const accountId = computed(() => props.account?.id ?? 0)
const currentConcurrency = computed(() => props.account?.current_concurrency ?? 0)
const aggregateConcurrency = ref(0)
const aggregateBaseline = ref(0)
const aggregateDirty = computed(() => aggregateConcurrency.value !== aggregateBaseline.value)
const configuredLaneTotal = computed(() => lanes.value.reduce((sum, lane) => sum + (lane.concurrency > 0 ? lane.concurrency : 0), 0))

const emptyForm = () => ({
  name: '',
  transport: 'proxy' as AccountProxyLaneTransport,
  proxy_id: null as number | null,
  concurrency: 1,
  weight: 1,
  priority: 50,
  status: 'active' as AccountProxyLaneStatus,
  schedulable: true
})
const form = reactive(emptyForm())

const resetForm = () => {
  Object.assign(form, emptyForm())
  if (props.account?.proxy_id != null) form.proxy_id = props.account.proxy_id
  else if (props.proxies.length > 0) form.proxy_id = props.proxies[0].id
  formError.value = ''
  editingLaneId.value = null
  formOpen.value = false
}

const syncAggregateConcurrency = () => {
  aggregateBaseline.value = Math.max(0, Math.floor(props.account?.concurrency ?? 0))
  aggregateConcurrency.value = aggregateBaseline.value
}

const saveAggregate = async () => {
  if (!accountId.value || !aggregateDirty.value) return
  aggregateSaving.value = true
  try {
    const value = Number.isFinite(aggregateConcurrency.value) && aggregateConcurrency.value >= 0
      ? Math.floor(aggregateConcurrency.value)
      : 0
    aggregateConcurrency.value = value
    const updated = await adminAPI.accounts.update(accountId.value, { concurrency: value })
    aggregateConcurrency.value = updated.concurrency
    aggregateBaseline.value = updated.concurrency
    appStore.showSuccess(t('admin.accounts.proxyLanes.aggregateSaved'))
    emit('updated')
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error) || t('admin.accounts.proxyLanes.aggregateSaveFailed'))
  } finally {
    aggregateSaving.value = false
  }
}

const loadLanes = async () => {
  if (!accountId.value) return
  loading.value = true
  loadError.value = ''
  try {
    lanes.value = await adminAPI.proxyLanes.list(accountId.value)
  } catch (error) {
    loadError.value = extractApiErrorMessage(error) || t('admin.accounts.proxyLanes.loadFailed')
  } finally {
    loading.value = false
  }
}

const startCreate = () => {
  resetForm()
  form.name = `IP${lanes.value.length + 1}`
  formOpen.value = true
}

const startEdit = (lane: AccountProxyLane) => {
  formOpen.value = true
  editingLaneId.value = lane.id
  form.name = lane.name
  form.transport = lane.transport
  form.proxy_id = lane.proxy_id
  form.concurrency = lane.concurrency
  form.weight = lane.weight
  form.priority = lane.priority
  form.status = lane.status
  form.schedulable = lane.schedulable
  formError.value = ''
}

const cancelEdit = () => resetForm()

const handleTransportChange = () => {
  if (form.transport === 'direct') form.proxy_id = null
  else if (form.proxy_id == null && props.proxies.length > 0) form.proxy_id = props.proxies[0].id
}

const normalizedNumber = (value: number, fallback: number) => Number.isFinite(value) && value >= 0 ? Math.floor(value) : fallback

const saveLane = async () => {
  formError.value = ''
  if (!form.name.trim()) {
    formError.value = t('admin.accounts.proxyLanes.invalidName')
    return
  }
  if (form.transport === 'proxy' && form.proxy_id == null) {
    formError.value = t('admin.accounts.proxyLanes.proxyRequired')
    return
  }
  const payload: AccountProxyLanePayload = {
    name: form.name.trim(),
    transport: form.transport,
    proxy_id: form.transport === 'proxy' ? form.proxy_id : null,
    concurrency: normalizedNumber(form.concurrency, 1),
    weight: normalizedNumber(form.weight, 1),
    priority: normalizedNumber(form.priority, 50),
    status: form.status,
    schedulable: form.schedulable
  }
  saving.value = true
  try {
    if (editingLaneId.value == null) {
      await adminAPI.proxyLanes.create(accountId.value, payload)
      appStore.showSuccess(t('admin.accounts.proxyLanes.created'))
    } else {
      await adminAPI.proxyLanes.update(accountId.value, editingLaneId.value, payload)
      appStore.showSuccess(t('admin.accounts.proxyLanes.saved'))
    }
    await loadLanes()
    resetForm()
    emit('updated')
  } catch (error) {
    formError.value = extractApiErrorMessage(error) || t('admin.accounts.proxyLanes.saveFailed')
  } finally {
    saving.value = false
  }
}

const deleteLane = async (lane: AccountProxyLane) => {
  if (!window.confirm(t('admin.accounts.proxyLanes.deleteConfirm', { name: lane.name }))) return
  busyLaneId.value = lane.id
  try {
    await adminAPI.proxyLanes.remove(accountId.value, lane.id)
    lanes.value = lanes.value.filter(item => item.id !== lane.id)
    if (editingLaneId.value === lane.id) resetForm()
    appStore.showSuccess(t('admin.accounts.proxyLanes.deleted'))
    emit('updated')
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error) || t('admin.accounts.proxyLanes.deleteFailed'))
  } finally {
    busyLaneId.value = null
  }
}

const laneEndpoint = (lane: AccountProxyLane) => {
  if (lane.transport === 'direct') return t('admin.accounts.proxyLanes.directEndpoint')
  if (lane.proxy) return `${lane.proxy.name} · ${lane.proxy.protocol}://${lane.proxy.host}:${lane.proxy.port}`
  return lane.proxy_id ? `Proxy #${lane.proxy_id}` : t('admin.accounts.proxyLanes.unknownEndpoint')
}

const transportClass = (transport: AccountProxyLaneTransport) => transport === 'direct'
  ? 'bg-slate-100 text-slate-700 dark:bg-slate-700 dark:text-slate-200'
  : 'bg-blue-100 text-blue-700 dark:bg-blue-500/20 dark:text-blue-300'

const statusClass = (status: AccountProxyLaneStatus) => ({
  active: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-300',
  paused: 'bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-300',
  error: 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-300',
  disabled: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300'
}[status] ?? 'bg-gray-100 text-gray-600')

const statusLabel = (status: AccountProxyLaneStatus) => ({
  active: t('admin.accounts.proxyLanes.statusActive'),
  paused: t('admin.accounts.proxyLanes.statusPaused'),
  error: t('admin.accounts.proxyLanes.statusError'),
  disabled: t('admin.accounts.proxyLanes.statusDisabled')
}[status] ?? status)

const handleClose = () => {
  if (saving.value) return
  resetForm()
  emit('close')
}

watch(
  () => [props.show, props.account?.id] as const,
  ([show]) => {
    if (show) {
      syncAggregateConcurrency()
      resetForm()
      void loadLanes()
    }
  },
  { immediate: true }
)
</script>
