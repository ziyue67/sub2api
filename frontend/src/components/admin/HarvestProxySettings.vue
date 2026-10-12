<template>
  <section class="card p-5" data-testid="harvest-proxy-settings">
    <p v-if="error" role="alert" class="mb-3 text-sm text-red-600">{{ error }}</p>
    <p v-if="loading" class="text-sm text-gray-500">{{ text('加载中…', 'Loading…') }}</p>
    <button v-else-if="!loaded" type="button" class="btn btn-secondary" @click="load">{{ text('重试', 'Retry') }}</button>
    <form v-else @submit.prevent="save">
      <fieldset :disabled="saving">
        <div>
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t("admin.settings.gatewayForwarding.codexTicketHarvestProxy") }}
          </h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t("admin.settings.gatewayForwarding.codexTicketHarvestProxyDesc") }}
          </p>
          <div class="mt-3 flex flex-wrap gap-2" role="radiogroup" :aria-label="t('admin.settings.gatewayForwarding.codexTicketProxyMode')">
            <label
              class="inline-flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors"
              :class="codexTicketProxyMode === 'mihomo'
                ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
                : 'border-gray-200 text-gray-600 hover:border-primary-300 dark:border-dark-600 dark:text-gray-400'"
            >
              <input
                class="sr-only"
                type="radio"
                name="codex-ticket-proxy-mode"
                value="mihomo"
                :checked="codexTicketProxyMode === 'mihomo'"
                @change="selectCodexTicketProxyMode('mihomo')"
              />
              <span>{{ t("admin.settings.gatewayForwarding.codexTicketProxyModeMihomo") }}</span>
            </label>
            <label
              class="inline-flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors"
              :class="codexTicketProxyMode === 'static'
                ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
                : 'border-gray-200 text-gray-600 hover:border-primary-300 dark:border-dark-600 dark:text-gray-400'"
            >
              <input
                class="sr-only"
                type="radio"
                name="codex-ticket-proxy-mode"
                value="static"
                :checked="codexTicketProxyMode === 'static'"
                @change="selectCodexTicketProxyMode('static')"
              />
              <span>{{ t("admin.settings.gatewayForwarding.codexTicketProxyModeStatic") }}</span>
            </label>
            <label
              class="inline-flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors"
              :class="codexTicketProxyMode === 'ip_pool'
                ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
                : 'border-gray-200 text-gray-600 hover:border-primary-300 dark:border-dark-600 dark:text-gray-400'"
            >
              <input
                class="sr-only"
                type="radio"
                name="codex-ticket-proxy-mode"
                value="ip_pool"
                :checked="codexTicketProxyMode === 'ip_pool'"
                @change="selectCodexTicketProxyMode('ip_pool')"
              />
              <span>{{ t("admin.settings.gatewayForwarding.codexTicketProxyModeIPPool") }}</span>
            </label>
          </div>
          <div
            v-if="codexTicketProxyMode === 'mihomo'"
            class="mt-3 rounded-md border border-primary-200 bg-primary-50/60 px-3 py-2.5 dark:border-primary-800 dark:bg-primary-900/20"
          >
            <div class="flex flex-wrap items-center gap-2 text-sm text-primary-800 dark:text-primary-200">
              <span>{{ t("admin.settings.gatewayForwarding.codexTicketProxyMihomoEndpoint") }}</span>
              <code class="rounded bg-white/70 px-1.5 py-0.5 font-mono text-xs dark:bg-dark-800/70">{{ CODEX_TICKET_MIHOMO_PROXY_URL }}</code>
            </div>
            <MihomoProxySelector @ready="selectMihomoHarvestProxy" />
          </div>
          <p
            v-else-if="codexTicketProxyMode === 'ip_pool'"
            data-testid="codex-ticket-proxy-ip-pool-hint"
            class="mt-3 rounded-md border border-primary-200 bg-primary-50/60 px-3 py-2.5 text-sm text-primary-800 dark:border-primary-800 dark:bg-primary-900/20 dark:text-primary-200"
          >
            {{ t("admin.settings.gatewayForwarding.codexTicketProxyIPPoolHint") }}
          </p>
          <input
            v-else
            id="codex-ticket-harvest-proxy"
            v-model="form.openai_codex_ticket_harvest_proxy_url"
            type="text"
            class="input mt-3 w-full font-mono text-sm"
            :placeholder="t('admin.settings.gatewayForwarding.codexTicketHarvestProxyPlaceholder')"
            autocomplete="off"
          />
          <p
            v-if="form.openai_codex_ticket_harvest_proxy_configured && codexTicketProxyMode === 'static'"
            class="mt-1.5 text-xs text-gray-500 dark:text-gray-400"
          >
            {{ t("admin.settings.gatewayForwarding.codexTicketHarvestProxyConfigured") }}
          </p>
        </div>
      </fieldset>
      <button type="submit" data-testid="save-harvest-proxy" class="btn btn-primary btn-sm mt-4" :disabled="saving || !canSave">{{ text('保存采票出口', 'Save harvest exit') }}</button>
    </form>
  </section>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getSettings, updateSettings } from '@/api/admin/settings'
import { useAppStore } from '@/stores'
import MihomoProxySelector from '@/views/admin/settings/MihomoProxySelector.vue'
const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const appStore = useAppStore()
const form = ref({ openai_codex_ticket_harvest_proxy_url: '', openai_codex_ticket_harvest_proxy_configured: false, openai_codex_ticket_static_proxy_url: '' })
const loading = ref(true)
const loaded = ref(false)
const saving = ref(false)
const error = ref('')
let disposed = false
const CODEX_TICKET_MIHOMO_PROXY_URL = "http://127.0.0.1:3101";
const CODEX_TICKET_IP_POOL_PROXY_URL = "ippool://active";
type CodexTicketProxyMode = "mihomo" | "static" | "ip_pool";
const codexTicketProxyMode = ref<CodexTicketProxyMode>("static");
const codexTicketStaticProxyDraft = ref("");

function isCodexTicketMihomoProxyURL(value: string): boolean {
  return value.trim().replace(/\/+$/, "") === CODEX_TICKET_MIHOMO_PROXY_URL;
}

function isCodexTicketIPPoolProxyURL(value: string): boolean {
  return value.trim() === CODEX_TICKET_IP_POOL_PROXY_URL;
}

function codexTicketProxyModeOf(value: string): CodexTicketProxyMode {
  if (isCodexTicketMihomoProxyURL(value)) return "mihomo";
  if (isCodexTicketIPPoolProxyURL(value)) return "ip_pool";
  return "static";
}

function selectMihomoHarvestProxy(endpoint: string): void {
  form.value.openai_codex_ticket_harvest_proxy_url = endpoint;
  appStore.showSuccess(t("admin.settings.gatewayForwarding.codexTicketProxyMihomoSelected"));
}

function syncCodexTicketProxyMode(): void {
  const current = form.value.openai_codex_ticket_harvest_proxy_url;
  const mode = codexTicketProxyModeOf(current);
  codexTicketProxyMode.value = mode;
  codexTicketStaticProxyDraft.value = mode === "static" ? current : form.value.openai_codex_ticket_static_proxy_url || "";
}

function selectCodexTicketProxyMode(mode: CodexTicketProxyMode): void {
  const previous = codexTicketProxyModeOf(form.value.openai_codex_ticket_harvest_proxy_url);
  if (previous === "static") {
    codexTicketStaticProxyDraft.value =
      form.value.openai_codex_ticket_harvest_proxy_url;
  }
  codexTicketProxyMode.value = mode;
  if (mode === "ip_pool") {
    form.value.openai_codex_ticket_harvest_proxy_url = CODEX_TICKET_IP_POOL_PROXY_URL;
  } else if (previous !== "static" && previous !== mode) {
    // Mihomo writes its endpoint only once the kernel reports ready, so until
    // then the form keeps the static address rather than the pool sentinel.
    form.value.openai_codex_ticket_harvest_proxy_url =
      codexTicketStaticProxyDraft.value;
  }
}

const canSave = computed(() => codexTicketProxyMode.value !== 'mihomo' || isCodexTicketMihomoProxyURL(form.value.openai_codex_ticket_harvest_proxy_url))
function apply(settings: Awaited<ReturnType<typeof getSettings>>) {
  form.value = {
    openai_codex_ticket_harvest_proxy_url: settings.openai_codex_ticket_harvest_proxy_url || '',
    openai_codex_ticket_harvest_proxy_configured: settings.openai_codex_ticket_harvest_proxy_configured ?? false,
    openai_codex_ticket_static_proxy_url: settings.openai_codex_ticket_static_proxy_url || ''
  }
  syncCodexTicketProxyMode()
}
async function load() {
  loading.value = true
  error.value = ''
  try {
    const settings = await getSettings()
    if (disposed) return
    apply(settings)
    loaded.value = true
  } catch {
    if (!disposed) error.value = text('无法加载采票出口，请重试。', 'Could not load harvest exit. Please retry.')
  } finally { if (!disposed) loading.value = false }
}
async function save() {
  if (!loaded.value || saving.value || !canSave.value) return
  saving.value = true
  error.value = ''
  try {
    const settings = await updateSettings({
      openai_codex_ticket_harvest_proxy_url: form.value.openai_codex_ticket_harvest_proxy_url.trim(),
      openai_codex_ticket_use_saved_static_proxy: codexTicketProxyMode.value === 'static'
    })
    if (disposed) return
    apply(settings)
    appStore.showSuccess(text('采票出口已保存。', 'Harvest exit saved.'))
  } catch {
    if (!disposed) error.value = text('采票出口保存失败，当前修改已保留。', 'Could not save harvest exit. Your changes are retained.')
  } finally { if (!disposed) saving.value = false }
}
onMounted(load)
onBeforeUnmount(() => { disposed = true })
</script>
