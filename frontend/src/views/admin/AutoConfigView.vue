<template>
  <AppLayout>
    <div class="space-y-5">
      <header><h1 class="text-2xl font-semibold">{{ t('autoConfig.title') }}</h1><p class="mt-2 text-sm text-gray-500">{{ t('autoConfig.description') }}</p></header>
      <SmartOpsNav />
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
      <p v-if="notice" role="status" class="text-emerald-600">{{ notice }}</p>
      <p v-if="loading">{{ t('common.loading') }}</p>
      <button v-if="!draft && !loading" class="btn btn-secondary" @click="load">{{ t('autoConfig.retry') }}</button>
      <form v-if="draft" class="space-y-5" @submit.prevent="save">
        <p v-if="draft.runtime_blocked" role="alert" class="rounded-xl bg-amber-50 p-4 text-amber-800">{{ t('autoConfig.blocked') }}</p>
        <fieldset :disabled="saving" class="grid min-w-0 gap-5 xl:grid-cols-2">
          <section class="card space-y-5">
            <header class="flex items-center justify-between gap-3"><h2 class="text-lg font-semibold">{{ t('autoConfig.initial') }}</h2><label class="flex items-center gap-2 text-sm"><input v-model="draft.enabled" data-testid="initial-enabled" type="checkbox" role="switch" />{{ t('autoConfig.enable') }}</label></header>
            <p class="text-sm leading-6 text-gray-500">{{ t('autoConfig.initialHint') }}</p>
            <label class="block"><span class="field-label">{{ t('autoConfig.platform') }}</span><select v-model="draft.platform" data-testid="platform" class="input w-full" @change="draft.group_ids = []"><option v-for="p in platforms" :key="p.value" :value="p.value">{{ p.label }}</option></select></label>
            <div class="grid gap-4 sm:grid-cols-3"><label v-for="field in initialFields" :key="field.key"><span class="field-label">{{ t('autoConfig.' + field.key) }}</span><input v-model.number="draft[field.key]" :data-testid="field.key" type="number" :min="field.min" max="10000" step="1" required class="input w-full" /></label></div>
            <p class="text-xs leading-5 text-gray-500">{{ t('autoConfig.loadHint') }}</p>
            <fieldset><legend class="field-label">{{ t('autoConfig.groups') }}</legend><div class="group-list"><label v-for="g in initialGroups" :key="g.id" class="group-option"><input v-model="draft.group_ids" :value="g.id" type="checkbox" :data-testid="'initial-group-' + g.id" /><span>{{ g.name }} <small class="text-gray-400">#{{ g.id }}</small></span></label><p v-if="!initialGroups.length" class="text-sm text-gray-500">{{ t('autoConfig.noGroups') }}</p></div></fieldset>
          </section>
          <section class="card space-y-5">
            <header class="flex items-center justify-between gap-3"><h2 class="text-lg font-semibold">{{ t('autoConfig.upgrade') }}</h2><label class="flex items-center gap-2 text-sm"><input v-model="draft.upgrade_enabled" data-testid="upgrade-enabled" type="checkbox" role="switch" />{{ t('autoConfig.enable') }}</label></header>
            <p class="text-sm leading-6 text-gray-500">{{ t('autoConfig.upgradeHint') }}</p>
            <div class="grid gap-4 sm:grid-cols-2"><label v-for="field in upgradeFields" :key="field.key"><span class="field-label">{{ t('autoConfig.' + field.key) }}</span><input v-model.number="draft[field.key]" :data-testid="field.key" type="number" min="1" :max="field.max" step="1" required class="input w-full" /></label></div>
            <p class="rounded-xl bg-emerald-50 p-3 text-sm leading-6 text-emerald-800 dark:bg-emerald-950/30 dark:text-emerald-200">{{ t('autoConfig.rule', { count: draft.successes_per_step, step: draft.upgrade_step, max: draft.max_concurrency }) }}</p>
            <fieldset><legend class="field-label">{{ t('autoConfig.upgradeGroups') }}</legend><div class="group-list"><label v-for="g in groups" :key="g.id" class="group-option"><input v-model="draft.upgrade_group_ids" :value="g.id" type="checkbox" :data-testid="'upgrade-group-' + g.id" /><span>{{ g.name }} <small class="text-gray-400">{{ g.platform }} · #{{ g.id }}</small></span></label></div></fieldset>
          </section>
        </fieldset>
        <div class="flex flex-wrap items-center gap-4"><button type="submit" class="btn btn-primary" :disabled="saving">{{ t(saving ? 'autoConfig.saving' : 'common.save') }}</button><span class="text-xs text-gray-500">{{ t('autoConfig.saveHint') }}</span></div>
      </form>
      <AutoConfigHistory v-if="auth.user?.role === 'admin'" :refresh-key="historyRefreshKey" />
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import AppLayout from '@/components/layout/AppLayout.vue'
import SmartOpsNav from '@/components/admin/operations/SmartOpsNav.vue'
import AutoConfigHistory from '@/components/admin/operations/AutoConfigHistory.vue'
import { getAll } from '@/api/admin/groups'
import { getAutoConfig, saveAutoConfig, type AutoConfig } from '@/api/admin/autoConfig'
import type { Group } from '@/types'
const { t } = useI18n(), auth = useAuthStore()
const draft = ref<AutoConfig | null>(null), groups = ref<Group[]>([])
const loading = ref(false), saving = ref(false), error = ref(''), notice = ref('')
const historyRefreshKey = ref(0)
let generation = 0, alive = true
const validGeneration = (v: number) => alive && generation === v
const platforms = [{ value: 'openai', label: 'OpenAI' }, { value: 'anthropic', label: 'Anthropic' }, { value: 'gemini', label: 'Gemini' }, { value: 'antigravity', label: 'Antigravity' }, { value: 'grok', label: 'Grok' }]
const initialFields = [{ key: 'priority', min: 0 }, { key: 'load_factor', min: 1 }, { key: 'concurrency', min: 1 }] as const
const upgradeFields = [{ key: 'successes_per_step', max: 100000 }, { key: 'upgrade_step', max: 1000 }, { key: 'max_concurrency', max: 10000 }, { key: 'cooldown_seconds', max: 86400 }] as const
const initialGroups = computed(() => groups.value.filter(g => g.platform === draft.value?.platform))
async function load() {
 const v = generation; loading.value = true; error.value = ''
 try { const [config, available] = await Promise.all([getAutoConfig(), getAll()]); if (validGeneration(v)) { draft.value = config; groups.value = available.filter(g => g.status === 'active') } }
 catch { if (validGeneration(v)) error.value = t('autoConfig.loadFailed') }
 finally { if (validGeneration(v)) loading.value = false }
}
async function save() {
 if (!draft.value || saving.value) return
 const c = draft.value
 if (c.enabled && !c.group_ids.length || c.upgrade_enabled && !c.upgrade_group_ids.length || [...initialFields, ...upgradeFields].some(f => !Number.isInteger(c[f.key]) || c[f.key] < ('min' in f ? f.min : 1) || c[f.key] > ('max' in f ? f.max : 10000))) { error.value = t('autoConfig.invalid'); return }
 const v = generation; saving.value = true; error.value = ''; notice.value = ''
 try { const { runtime_blocked: _blocked, ...payload } = c; const result = await saveAutoConfig(payload); if (validGeneration(v)) { draft.value = result; notice.value = t('autoConfig.saved'); historyRefreshKey.value++ } }
 catch { if (validGeneration(v)) error.value = t('autoConfig.saveFailed') }
 finally { if (validGeneration(v)) saving.value = false }
}
watch(() => auth.user ? auth.user.id + ':' + auth.user.role : '', () => { generation++; draft.value = null; groups.value = []; error.value = notice.value = ''; loading.value = saving.value = false }, { flush: 'sync' })
onMounted(() => { void load() })
onBeforeUnmount(() => { alive = false; generation++ })
</script>
<style scoped>
.card { @apply min-w-0 rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-900; }
.field-label { @apply mb-2 block text-sm font-medium text-gray-600 dark:text-gray-300; }
.group-list { @apply max-h-48 space-y-2 overflow-y-auto rounded-xl border border-gray-200 p-3 dark:border-dark-700; }
.group-option { @apply flex items-center gap-2 text-sm; }
input[type=checkbox] { @apply h-4 w-4 shrink-0 rounded text-primary-600; }
</style>
