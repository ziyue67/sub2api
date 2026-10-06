<template>
  <section class="card overflow-hidden" aria-labelledby="gateway-borrow-title" data-testid="gateway-borrow-panel">
    <div class="flex flex-wrap items-start justify-between gap-4 p-5">
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-2">
          <h2 id="gateway-borrow-title" class="text-sm font-semibold text-gray-900 dark:text-white">{{ t(`${p}.borrowTitle`) }}</h2>
          <span class="rounded-full px-2 py-0.5 text-xs" :class="saved?.cookie_pool.enabled ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-700'">{{ t(loading && !saved ? 'common.loading' : saved?.cookie_pool.enabled ? `${p}.enabled` : `${p}.disabled`) }}</span>
          <span v-if="dirty && draft" class="text-xs text-amber-600">{{ t(`${p}.unsaved`) }}</span>
        </div>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t(`${p}.borrowDescription`) }}</p>
        <p v-if="saved" class="mt-2 text-xs text-gray-400">{{ t(`${p}.borrowSummary`, { sources: saved.cookie_pool.source_account_ids.length, targets: saved.cookie_pool.target_account_ids.length }) }}</p>
        <p v-if="error && !expanded" role="alert" class="mt-2 text-sm text-red-600">{{ error }}</p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" data-testid="expand-borrow" :aria-expanded="expanded" aria-controls="gateway-borrow-content" @click="expanded = !expanded">{{ t(expanded ? `${p}.collapse` : `${p}.expand`) }}</button>
    </div>
    <div v-if="expanded" id="gateway-borrow-content" class="space-y-5 border-t border-gray-100 p-5 dark:border-dark-700">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t(`${p}.configuration`) }}</h3>
          <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t(`${p}.description`) }}</p>
        </div>
        <div class="flex items-center gap-2">
          <span v-if="dirty" class="text-xs text-amber-600">{{ t(`${p}.unsaved`) }}</span>
          <button class="btn btn-secondary" type="button" :disabled="loading || saving" @click="load">{{ t(dirty ? `${p}.discard` : 'common.refresh') }}</button>
          <button data-testid="save" class="btn btn-primary" type="submit" form="astra-gateway-form" :disabled="!draft || !dirty || saving || !!validation">{{ t(saving ? `${p}.saving` : `${p}.save`) }}</button>
        </div>
      </header>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <p class="rounded-xl border border-primary-200 bg-primary-50 p-4 text-sm dark:border-primary-800 dark:bg-primary-900/20">{{ t(`${p}.automaticHint`) }}</p>
      <p v-if="savedMessage" role="status" class="rounded-xl bg-emerald-50 p-4 text-sm text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300">{{ t(`${p}.saved`) }}</p>
      <div v-if="loading && !draft" class="flex justify-center py-16"><LoadingSpinner /></div>
      <form v-if="draft" id="astra-gateway-form" class="space-y-6" @submit.prevent="save">
        <fieldset :disabled="saving || loading" class="space-y-6">
          <section class="card p-5 sm:p-6" aria-labelledby="astra-cookie-title">
            <div class="flex items-start justify-between gap-4">
              <div>
                <h2 id="astra-cookie-title" class="font-semibold text-gray-900 dark:text-white">{{ t(`${p}.cookieTitle`) }}</h2>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t(`${p}.cookieDescription`) }}</p>
              </div>
              <Toggle v-model="draft.cookie_pool.enabled" data-testid="cookie-toggle" :aria-label="t(`${p}.cookieTitle`)" />
            </div>
            <p class="mt-3 text-xs text-gray-500">{{ t(`${p}.savedState`) }}：{{ t(saved?.cookie_pool.enabled ? `${p}.enabled` : `${p}.disabled`) }}</p>
            <div class="mt-5 grid gap-4 md:grid-cols-2">
              <AstraAccountPicker v-model="draft.cookie_pool.source_account_ids" :label="t(`${p}.sources`)" :accounts="accounts" :disabled="saving" />
              <AstraAccountPicker v-model="draft.cookie_pool.target_account_ids" :label="t(`${p}.targets`)" :accounts="accounts" :disabled="saving" />
            </div>
            <div class="mt-4 flex items-start justify-between gap-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
              <div>
                <h3 class="text-sm font-medium">{{ t(`${p}.ipAffinity`) }}</h3>
                <p id="astra-ip-affinity-hint" class="mt-1 text-xs leading-6 text-gray-500 dark:text-gray-400">{{ t(`${p}.ipAffinityHint`) }}</p>
              </div>
              <Toggle :model-value="draft.cookie_pool.ip_affinity ?? false" @update:model-value="setAffinity" data-testid="ip-affinity-toggle" :aria-label="t(`${p}.ipAffinity`)" aria-describedby="astra-ip-affinity-hint" />
            </div>
            <div class="mt-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
              <div class="flex items-start justify-between gap-4">
                <div><h3 class="text-sm font-medium">{{ t(`${p}.rotateNodes`) }}</h3><p id="astra-rotation-hint" class="mt-1 text-xs leading-6 text-gray-500 dark:text-gray-400">{{ t(`${p}.rotateNodesHint`) }}</p></div>
                <Toggle :model-value="draft.cookie_pool.rotate_nodes ?? false" @update:model-value="setRotation" data-testid="rotation-toggle" :aria-label="t(`${p}.rotateNodes`)" aria-describedby="astra-rotation-hint" />
              </div>
              <label v-if="draft.cookie_pool.rotate_nodes" class="mt-3 flex items-center gap-3 text-sm">{{ t(`${p}.maxNodeAttempts`) }}<input v-model.number="draft.cookie_pool.max_node_attempts" data-testid="node-attempts" class="input w-24" type="number" min="1" max="10" required /></label>
              <label v-if="draft.cookie_pool.rotate_nodes" class="mt-3 flex items-center gap-3 text-sm">{{ t(`${p}.nodeCooldown`) }}<input v-model.number="draft.cookie_pool.node_cooldown_seconds" data-testid="node-cooldown" class="input w-28" type="number" min="60" max="86400" required /> s</label>
              <p v-if="draft.cookie_pool.rotate_nodes" class="mt-2 text-xs text-gray-500">{{ t(`${p}.nodeCooldownHint`) }}</p>
            </div>
            <label class="mt-4 flex items-center gap-3 text-sm">{{ t(`${p}.cookieTTL`) }}<input v-model.number="draft.cookie_pool.ttl_seconds" class="input w-28" type="number" min="30" max="240" required /> s</label>
            <p class="mt-4 text-xs leading-6 text-gray-500 dark:text-gray-400">{{ t(`${p}.cookieHint`) }}</p>
          </section>
          <section class="card p-5 sm:p-6">
            <div class="flex items-start justify-between gap-4">
              <div>
                <h2 class="font-semibold">{{ t(`${p}.accountScheduling`) }}</h2>
                <p class="mt-2 text-sm text-gray-500">{{ t(`${p}.accountSchedulingHint`) }}</p>
              </div>
              <Toggle :model-value="draft.account_scheduling ?? false" @update:model-value="draft.account_scheduling = $event" data-testid="account-scheduling-toggle" :aria-label="t(`${p}.accountScheduling`)" />
            </div>
            <div class="mt-4 space-y-3">
              <label class="block text-sm">{{ t(`${p}.schedulingMode`) }}
                <select v-model="draft.scheduling_mode" class="input mt-1" data-testid="scheduling-mode">
                  <option value="model">{{ t(`${p}.modeModel`) }}</option>
                  <option value="account">{{ t(`${p}.modeAccount`) }}</option>
                  <option value="groups">{{ t(`${p}.modeGroups`) }}</option>
                </select>
              </label>
              <div v-if="draft.scheduling_mode === 'groups'" data-testid="scheduling-groups">
                <p class="text-sm">{{ t(`${p}.schedulingGroups`) }}</p>
                <p v-if="groupsError" class="text-sm text-red-600">{{ groupsError }}</p>
                <label v-for="group in groups" :key="group.id" class="mr-4 inline-flex items-center gap-2 text-sm">
                  <input v-model="draft.scheduling_group_ids" type="checkbox" :value="group.id" />{{ group.name }} (#{{ group.id }})
                </label>
              </div>
            </div>
            <div class="mt-4 border-t pt-3 dark:border-dark-600" data-testid="scheduling-records">
              <h3 class="text-sm font-medium">{{ t(`${p}.schedulingLog`) }}</h3>
              <p class="mt-1 text-xs text-gray-500">{{ t(`${p}.schedulingLogHint`) }}</p>
              <p v-if="!schedulingRecords.length" class="mt-2 text-sm text-gray-500">{{ t(`${p}.schedulingLogEmpty`) }}</p>
              <ul v-else class="mt-2 space-y-2 text-sm">
                <li v-for="(row, index) in schedulingRecords.slice(0, 3)" :key="index" class="flex flex-wrap gap-x-3 gap-y-1">
                  <time>{{ new Date(row.checked_at).toLocaleString() }}</time><span>#{{ row.account_id }}</span>
                  <span>{{ t(`${p}.${row.mode === 'model' ? 'modeModel' : row.mode === 'groups' ? 'modeGroups' : 'modeAccount'}`) }}</span>
                  <span :class="row.schedulable ? 'text-emerald-600' : 'text-amber-600'">{{ t(row.schedulable ? `${p}.enabled` : `${p}.disabled`) }}</span>
                  <span class="text-gray-500">{{ te(`${p}.reasons.${row.reason}`) ? t(`${p}.reasons.${row.reason}`) : row.reason }}</span>
                </li>
              </ul>
            </div>
          </section>
          <section class="card p-5 sm:p-6" aria-labelledby="astra-ws-title">
            <div class="flex items-start justify-between gap-4">
              <div>
                <h2 id="astra-ws-title" class="font-semibold text-gray-900 dark:text-white">{{ t(`${p}.wsTitle`) }}</h2>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t(`${p}.wsDescription`) }}</p>
              </div>
              <Toggle v-model="draft.ws_session.enabled" data-testid="ws-toggle" :aria-label="t(`${p}.wsTitle`)" />
            </div>
            <p class="mt-3 text-xs text-gray-500">{{ t(`${p}.savedState`) }}：{{ t(saved?.ws_session.enabled ? `${p}.enabled` : `${p}.disabled`) }}</p>
            <div class="mt-5"><AstraAccountPicker v-model="draft.ws_session.account_ids" :label="t(`${p}.wsAccounts`)" :accounts="accounts" :disabled="saving" /></div>
            <label class="mt-4 flex items-center gap-3 text-sm">{{ t(`${p}.wsTTL`) }}<input v-model.number="draft.ws_session.ttl_seconds" class="input w-28" type="number" min="60" max="3600" required /> s</label>
            <p class="mt-4 text-xs leading-6 text-gray-500 dark:text-gray-400">{{ t(`${p}.wsHint`) }}</p>
          </section>
        </fieldset>
        <p v-if="validation" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ validation }}</p>
      </form>
      <AstraGatewayRuntime :key="saved?.revision" :settings="saved" :dirty="dirty" @scheduling="schedulingRecords = $event" />
      <details><summary class="cursor-pointer text-sm font-medium text-gray-600 dark:text-gray-300">{{ t(`${p}.historyTitle`) }}</summary><AstraGatewayHistory v-if="historyExpanded" /><button v-else type="button" class="btn btn-secondary btn-sm mt-3" @click="historyExpanded = true">{{ t(`${p}.historyQuery`) }}</button></details>
      <aside class="rounded-xl border border-gray-200 p-5 text-sm leading-6 text-gray-600 dark:border-dark-600 dark:text-gray-300">
        <p>{{ t(`${p}.boundary`) }}</p>
        <RouterLink to="/admin/accounts" class="mt-2 inline-block font-medium text-primary-600 hover:underline">{{ t(`${p}.manageAccounts`) }} →</RouterLink>
      </aside>
    </div>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { getAllIncludingInactive } from '@/api/admin/groups'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import Toggle from '@/components/common/Toggle.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import AstraGatewayRuntime from '@/components/admin/AstraGatewayRuntime.vue'
import AstraGatewayHistory from '@/components/admin/AstraGatewayHistory.vue'
import AstraAccountPicker from '@/components/admin/AstraAccountPicker.vue'
import { list } from '@/api/admin/accounts'
import { getAstraGateway, saveAstraGateway, normalizeAstraGateway, resolveAstraDependencies, type AstraGatewaySettings } from '@/api/admin/astraGateway'
const schedulingRecords = ref<{ checked_at: string; account_id: number; schedulable: boolean; reason: string; mode?: string }[]>([])
const p = 'admin.astraGateway'
const { t, te } = useI18n()
const expanded = ref(false)
const historyExpanded = ref(false)
const loading = ref(false)
const saving = ref(false)
const savedMessage = ref(false)
const error = ref('')
const saved = ref<AstraGatewaySettings>()
const draft = ref<AstraGatewaySettings>()
const groups = ref<{ id: number; name: string }[]>([])
const groupsError = ref('')
watch(() => draft.value?.scheduling_mode, async mode => {
 if (mode !== 'groups') return
 try { groups.value = await getAllIncludingInactive(); groupsError.value = '' } catch { groupsError.value = t(`${p}.loadError`) }
})
const accounts = ref<{ id: number; name: string }[]>([])
const dirty = computed(() => JSON.stringify(draft.value) !== JSON.stringify(saved.value))
const validation = computed(() => {
  if (!draft.value) return ''
  const value = resolveAstraDependencies(draft.value)
  if (value.account_scheduling && value.scheduling_mode === 'groups' && !value.scheduling_group_ids?.length) return t(`${p}.chooseSchedulingGroups`)
  if (value.cookie_pool.rotate_nodes && (!Number.isInteger(draft.value.cookie_pool.node_cooldown_seconds) || draft.value.cookie_pool.node_cooldown_seconds! < 60 || draft.value.cookie_pool.node_cooldown_seconds! > 86400)) return t(`${p}.nodeCooldownInvalid`)
  if (value.cookie_pool.rotate_nodes && (!Number.isInteger(draft.value.cookie_pool.max_node_attempts) || draft.value.cookie_pool.max_node_attempts! < 1 || draft.value.cookie_pool.max_node_attempts! > 10)) return t(`${p}.nodeAttemptsInvalid`)
  if (!Number.isInteger(value.cookie_pool.ttl_seconds) || value.cookie_pool.ttl_seconds! < 30 || value.cookie_pool.ttl_seconds! > 240 || !Number.isInteger(value.ws_session.ttl_seconds) || value.ws_session.ttl_seconds! < 60 || value.ws_session.ttl_seconds! > 3600) return t(`${p}.ttlInvalid`)
  if (value.cookie_pool.enabled) {
    if (!value.cookie_pool.source_account_ids.length || !value.cookie_pool.target_account_ids.length) return t(`${p}.chooseBoth`)
    if (value.cookie_pool.source_account_ids.some(id => value.cookie_pool.target_account_ids.includes(id))) return t(`${p}.overlap`)
  }
  if (value.ws_session.enabled && !value.ws_session.account_ids.length) return t(`${p}.chooseWS`)
  const enabledIDs = [...(value.cookie_pool.enabled ? [...value.cookie_pool.source_account_ids, ...value.cookie_pool.target_account_ids] : []), ...(value.ws_session.enabled ? value.ws_session.account_ids : [])]
  if (enabledIDs.some(id => !accounts.value.some(a => a.id === id))) return t(`${p}.missingAccounts`)
  return ''
})
function setAffinity(enabled: boolean) {
  if (!draft.value) return
  draft.value.cookie_pool.ip_affinity = enabled
  if (!enabled) { draft.value.cookie_pool.rotate_nodes = false; draft.value.cookie_pool.max_node_attempts = Math.min(10, Math.max(1, Math.trunc(draft.value.cookie_pool.max_node_attempts || 3))) }
}
function setRotation(enabled: boolean) {
  if (!draft.value) return
  draft.value.cookie_pool.rotate_nodes = enabled
  if (enabled) draft.value.cookie_pool.ip_affinity = true
}
async function load() {
  loading.value = true; error.value = ''; savedMessage.value = false
  try {
    const value = await getAstraGateway()
    const items: { id: number; name: string }[] = []
    for (let page = 1; ; page++) {
      const result = await list(page, 100, { platform: 'openai', type: 'oauth', lite: 'true' })
      items.push(...result.items.map(a => ({ id: a.id, name: a.name })))
      if (page >= result.pages || !result.items.length) break
    }
    accounts.value = items
    saved.value = normalizeAstraGateway(value); draft.value = normalizeAstraGateway(value)
  } catch { error.value = t(`${p}.loadError`) }
  finally { loading.value = false }
}
async function save() {
  if (!draft.value || validation.value || saving.value || loading.value || !dirty.value) return
  saving.value = true; error.value = ''; savedMessage.value = false
  try {
    const value = await saveAstraGateway(resolveAstraDependencies(draft.value))
    saved.value = normalizeAstraGateway(value); draft.value = normalizeAstraGateway(value); savedMessage.value = true
   } catch (err: unknown) {
    const message = (err as { response?: { data?: { message?: string } }; message?: string }).response?.data?.message || (err as { message?: string }).message || ''
    error.value = message.startsWith('astra_source_required') ? t(`${p}.chooseBoth`) : message.startsWith('astra_global_ws_disabled') ? t(`${p}.globalBlocked`) : message.startsWith('astra_account_unavailable') ? t(`${p}.missingAccounts`) : t(`${p}.saveError`)
  }
  finally { saving.value = false }
}
onMounted(load)
</script>
