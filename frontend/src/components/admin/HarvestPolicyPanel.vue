<template>
  <section class="card p-5" data-testid="harvest-policy-panel">
    <div class="mb-4 flex items-center justify-between gap-3">
      <h2 class="text-sm font-semibold">{{ localText('自动打票策略', 'Automatic harvest policy') }}</h2>
      <router-link to="/admin/proxies#harvest" class="text-sm text-primary-600">{{ localText('管理采票出口', 'Manage harvest exit') }}</router-link>
    </div>
    <p v-if="error" role="alert" class="mb-3 text-sm text-red-600">{{ error }}</p>
    <p v-if="loading" class="text-sm text-gray-500">{{ localText('加载中…', 'Loading…') }}</p>
    <button v-else-if="!form" type="button" class="btn btn-secondary" @click="load">{{ localText('重试', 'Retry') }}</button>
    <form v-else class="space-y-4" @submit.prevent="save">
      <fieldset class="space-y-4" :disabled="saving">
        <div class="flex items-center justify-between gap-4">
          <div class="min-w-0">
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t("admin.settings.gatewayForwarding.codexTicketEnabled") }}
            </h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t("admin.settings.gatewayForwarding.codexTicketEnabledDesc") }}
            </p>
          </div>
          <Toggle
            id="codex-ticket-enabled"
            v-model="form.openai_codex_ticket_enabled"
          />
        </div>
        <div class="flex items-center justify-between gap-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <div class="min-w-0">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t("admin.settings.gatewayForwarding.codexTicketFailClosed") }}
            </h3>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t("admin.settings.gatewayForwarding.codexTicketFailClosedDesc") }}
            </p>
          </div>
          <Toggle
            id="codex-ticket-fail-closed"
            v-model="form.openai_codex_ticket_fail_closed"
          />
        </div>
        <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t("admin.settings.gatewayForwarding.codexTicketModels") }}
          </h3>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t("admin.settings.gatewayForwarding.codexTicketModelsDesc") }}
          </p>

          <div class="mt-3 grid gap-3 sm:grid-cols-2">
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input
                id="codex-ticket-model-astra"
                type="checkbox"
                :checked="form.openai_codex_ticket_models.includes('gpt-6-astra')"
                @change="toggleCodexTicketModel('gpt-6-astra', ($event.target as HTMLInputElement).checked)"
              />
              <span>Astra (gpt-6-astra)</span>
            </label>
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input
                id="codex-ticket-model-sol"
                type="checkbox"
                :checked="form.openai_codex_ticket_models.includes('gpt-5.6-sol')"
                @change="toggleCodexTicketModel('gpt-5.6-sol', ($event.target as HTMLInputElement).checked)"
              />
              <span>Sol (gpt-5.6-sol)</span>
            </label>
          </div>
          <fieldset class="mt-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <legend class="px-1 text-sm font-semibold">{{ localText('自动打票范围', 'Automatic harvest scope') }}</legend>
            <select id="codex-ticket-harvest-scope" v-model="form.openai_codex_ticket_harvest_scope.mode" class="input" @change="changeHarvestScope">
              <option v-if="form.openai_codex_ticket_harvest_scope.mode === 'selected'" value="selected" disabled>{{ localText('旧分组范围（请选择账号范围）', 'Legacy group scope (choose an account scope)') }}</option>
              <option value="all">{{ localText('全部 OAuth 账号', 'All OAuth accounts') }}</option>
              <option value="selected_accounts">{{ localText('指定 OAuth 账号', 'Selected OAuth accounts') }}</option>
            </select>
            <p v-if="form.openai_codex_ticket_harvest_scope.mode === 'selected'" class="mt-2 text-xs text-amber-600">{{ localText('当前仍使用旧分组范围。切换并保存后，改为按 OAuth 账号打票。', 'The saved group scope remains active until you choose and save an OAuth account scope.') }}</p>
            <p v-else-if="form.openai_codex_ticket_harvest_scope.mode === 'all'" class="mt-2 text-xs text-gray-500">{{ localText('默认给全部 OAuth 账号打票，手动设为「不打票」的账号除外。新增 OAuth 账号自动纳入。', 'Harvest all OAuth accounts except those manually set to Skip harvest. New OAuth accounts are included by default.') }}</p>
            <p v-else-if="form.openai_codex_ticket_harvest_scope.mode === 'selected_accounts'" class="mt-2 text-xs text-gray-500">{{ localText('只给下方「门票账号」中明确开启打票的 OAuth 账号打票。不按分组筛选；未选择账号时不自动打票。', 'Only harvest OAuth accounts explicitly enabled in Ticket accounts below, regardless of groups. No accounts selected means no automatic harvesting.') }}</p>
            <p class="mt-2 text-xs text-gray-500">{{ localText('只控制给哪些账号自动打票。票据有效后的加组、恢复模型或恢复账号调度，仍由原来的「号池调度」设置控制。', 'Controls which accounts are harvested. The existing pool scheduling settings still control group membership, model restoration and account scheduling after ticket validation.') }}</p>
          </fieldset>
          <label class="mt-3 block text-sm" for="codex-ticket-strategy">{{ localText('票据刷新策略', 'Ticket refresh strategy') }}</label>
          <select id="codex-ticket-strategy" v-model="form.openai_codex_ticket_strategy" class="input mt-2">
            <option value="standby">{{ localText('提前准备备用（默认）', 'Prepare standby (default)') }}</option>
            <option value="fixed">{{ localText('固定主用，失效后再采集', 'Keep primary until invalid') }}</option>
          </select>
          <div class="mt-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <label class="flex items-center gap-2 text-sm" for="codex-ticket-candy-observation">
              <input id="codex-ticket-candy-observation" type="checkbox" v-model="form.openai_codex_ticket_candy_observation" />
              <span>{{ localText('启用糖果测试验证使用后的票据', 'Enable candy tests to observe tickets after use') }}</span>
              <span class="rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-800 dark:bg-amber-900/30 dark:text-amber-300">Beta</span>
            </label>
            <p class="mt-1 text-xs text-gray-500">{{ localText('默认关闭。SSE Astra 票据用于真实请求后，异步发送一发糖果题并记录结果，额外消耗上游额度。结果不影响调度、票据或账号状态。', 'Off by default. After an SSE Astra ticket serves a real request, asynchronously send one candy question and record the result, using additional upstream quota. Results do not change scheduling, tickets, or account state.') }}</p>
          </div>
          <label class="mt-3 flex items-center gap-2 text-sm"><input id="codex-ticket-strict" type="checkbox" v-model="form.openai_codex_ticket_strict_response" />{{ localText('严格拦截票据响应不匹配（默认关闭）', 'Reject mismatched ticket responses (off by default)') }}</label>
          <p class="mt-1 text-xs text-gray-500">{{ localText('仅在 HTTP 响应头明确不匹配时拦截正文；当前请求可能已计费，不会自动重放。关闭时只更新后续票据调度。', 'Rejects the body when HTTP response state headers mismatch. The upstream may have charged; the request is not replayed. When off, only future ticket scheduling changes.') }}</p>
          <p class="mt-1 text-xs text-gray-500">{{ localText('切换策略保留有效主备票据和冷却。固定主用可能在失效后短暂等待新票。', 'Switching preserves valid tickets and cooldowns. Fixed primary may briefly wait for a new ticket after expiry.') }}</p>

        </div>
      </fieldset>
      <button type="submit" data-testid="save-harvest-policy" class="btn btn-primary btn-sm" :disabled="saving">{{ localText('保存打票策略', 'Save harvest policy') }}</button>
    </form>
    <HarvestPoolSchedulingPanel />
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import HarvestPoolSchedulingPanel from './HarvestPoolSchedulingPanel.vue'
import { getSettings, updateSettings, type SystemSettings } from '@/api/admin/settings'
import { useAppStore } from '@/stores'

const emit = defineEmits<{ saved: [] }>()
const { t, locale } = useI18n()
const localText = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const appStore = useAppStore()
const saving = ref(false)
const loading = ref(true)
const error = ref('')
let disposed = false
function policyOf(settings: SystemSettings) {
  const scope = settings.openai_codex_ticket_harvest_scope
  return {
    openai_codex_ticket_enabled: settings.openai_codex_ticket_enabled ?? false,
    openai_codex_ticket_fail_closed: settings.openai_codex_ticket_fail_closed ?? false,
    openai_codex_ticket_models: [...(settings.openai_codex_ticket_models ?? ['gpt-6-astra', 'gpt-5.6-sol'])],
    openai_codex_ticket_strategy: settings.openai_codex_ticket_strategy || 'standby',
    openai_codex_ticket_candy_observation: settings.openai_codex_ticket_candy_observation ?? false,
    openai_codex_ticket_strict_response: settings.openai_codex_ticket_strict_response ?? false,
    openai_codex_ticket_harvest_scope: {
      mode: scope?.mode || 'all', group_ids: [...(scope?.group_ids ?? [])],
      account_policy: scope?.account_policy || 'schedulable_only'
    }
  }
}
const form = ref<ReturnType<typeof policyOf> | null>(null)
function changeHarvestScope() {
  if (!form.value) return
  form.value.openai_codex_ticket_harvest_scope.group_ids = []
  // Pool scheduling may disable an account until harvesting restores its ticket.
  form.value.openai_codex_ticket_harvest_scope.account_policy = 'prioritize_schedulable'
}
function toggleCodexTicketModel(model: string, checked: boolean) {
  if (!form.value) return
  const models = new Set(form.value.openai_codex_ticket_models)
  if (checked) models.add(model)
  else models.delete(model)
  form.value.openai_codex_ticket_models = [...models]
}
async function load() {
  loading.value = true
  error.value = ''
  const [settings] = await Promise.allSettled([getSettings()])
  if (disposed) return
  if (settings.status === 'fulfilled') form.value = policyOf(settings.value)
  else error.value = localText('无法加载打票策略，请重试。', 'Could not load harvest policy. Please retry.')
  loading.value = false
}
async function save() {
  if (!form.value || saving.value || loading.value) return
  saving.value = true
  error.value = ''
  try {
    await updateSettings(structuredClone({ ...form.value, openai_codex_ticket_models: [...form.value.openai_codex_ticket_models], openai_codex_ticket_harvest_scope: { ...form.value.openai_codex_ticket_harvest_scope, group_ids: [...form.value.openai_codex_ticket_harvest_scope.group_ids] } }))
    if (disposed) return
    appStore.showSuccess(localText('打票策略已保存。', 'Harvest policy saved.'))
    emit('saved')
  } catch {
    if (!disposed) error.value = localText('打票策略保存失败，当前修改已保留。', 'Could not save harvest policy. Your changes are retained.')
  } finally { if (!disposed) saving.value = false }
}
onMounted(load)
onBeforeUnmount(() => { disposed = true })
</script>
