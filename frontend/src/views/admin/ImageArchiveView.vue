<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('imageArchive.title') }}</h1>
          <p class="mt-2 text-sm text-gray-500">{{ t('imageArchive.description') }}</p>
        </div>
        <div class="flex gap-2">
          <button class="btn btn-secondary" :disabled="loading" @click="refresh">{{ t('common.refresh') }}</button>
          <button class="btn btn-primary" :disabled="!stats" @click="openSettings">{{ t('imageArchive.settings') }}</button>
        </div>
      </div>

      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>

      <div v-if="stats" class="card grid gap-5 p-5 sm:grid-cols-3">
        <div><p class="text-sm text-gray-500">{{ t('imageArchive.storage') }}</p><p class="mt-1 text-xl font-semibold">{{ bytes(stats.stored_bytes) }} / {{ bytes(stats.config.max_bytes) }}</p></div>
        <div><p class="text-sm text-gray-500">{{ t('imageArchive.retention') }}</p><p class="mt-1 text-xl font-semibold">{{ t('imageArchive.days', { n: stats.config.retention_days }) }}</p></div>
        <div><p class="text-sm text-gray-500">{{ t('imageArchive.capture') }}</p><p class="mt-1 text-xl font-semibold">{{ stats.config.enabled ? t('imageArchive.enabled') : t('imageArchive.disabled') }}</p></div>
        <div class="col-span-full text-xs text-gray-500">
          {{ t('imageArchive.runtime', { active: stats.active, failed: stats.failed, dropped: stats.dropped }) }}
          · {{ t('imageArchive.freeDisk') }} {{ bytes(stats.disk_free_bytes) }}
        </div>
      </div>
      <div v-if="stats && (stats.warning || !stats.storage_available)" role="status" class="rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200">
        {{ t('imageArchive.warning') }} {{ codes(stats.warning || 'storage_unavailable') }}
        <span v-if="stats.warning_at"> · {{ time(stats.warning_at) }}</span>
      </div>

      <form class="card grid gap-4 p-5 sm:grid-cols-3 lg:grid-cols-6" @submit.prevent="search">
        <label class="text-sm">{{ t('imageArchive.userID') }}<input v-model="filters.user" type="number" min="1" class="input mt-1" /></label>
        <label class="text-sm">{{ t('imageArchive.group') }}<select v-model="filters.group" class="input mt-1"><option value="">{{ t('imageArchive.all') }}</option><option v-for="group in groups" :key="group.id" :value="String(group.id)">{{ group.name }}</option></select></label>
        <label class="text-sm">{{ t('imageArchive.accountID') }}<input v-model="filters.account" type="number" min="1" class="input mt-1" /></label>
        <label class="text-sm">{{ t('imageArchive.model') }}<input v-model.trim="filters.model" class="input mt-1" /></label>
        <label class="text-sm">{{ t('imageArchive.from') }}<input v-model="filters.from" type="datetime-local" class="input mt-1" /></label>
        <label class="text-sm">{{ t('imageArchive.to') }}<input v-model="filters.to" type="datetime-local" class="input mt-1" /></label>
        <div class="col-span-full flex items-center gap-3"><button class="btn btn-primary" :disabled="loading">{{ t('common.search') }}</button><span class="text-sm text-gray-500">{{ t('imageArchive.total', { n: total }) }}</span></div>
      </form>

      <p v-if="loading" role="status" class="py-10 text-center text-gray-500">{{ t('common.loading') }}</p>
      <div v-else-if="records.length" class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
        <button v-for="record in records" :key="record.id" type="button" class="card overflow-hidden text-left transition-shadow hover:shadow-lg focus-visible:ring-2 focus-visible:ring-primary-500" @click="openRecord(record.id)">
          <div class="h-56 overflow-hidden"><ImageArchiveAsset v-if="cover(record)" :record-i-d="record.id" :file="cover(record)!" /><div v-else class="flex h-full items-center justify-center bg-gray-100 text-sm text-gray-500 dark:bg-dark-800">{{ t('imageArchive.noImages') }}</div></div>
          <div class="space-y-2 p-4">
            <div class="flex justify-between gap-2"><span class="truncate font-medium">{{ record.model }}</span><span class="shrink-0 text-xs" :class="record.status === 'saved' ? 'text-emerald-600' : 'text-amber-600'">{{ t(`imageArchive.status.${record.status}`) }}</span></div>
            <p class="line-clamp-2 min-h-10 whitespace-pre-wrap break-words text-sm text-gray-600 dark:text-gray-300">{{ record.prompt || '—' }}</p>
            <p class="truncate text-xs text-gray-500">{{ record.user_email || `#${record.user_id}` }} · {{ record.group_name || '—' }}</p>
            <p class="text-xs text-gray-400">{{ time(record.created_at) }} · {{ t('imageArchive.fileCount', { n: record.files.length }) }}</p>
          </div>
        </button>
      </div>
      <div v-else class="card p-12 text-center text-gray-500"><p class="font-medium">{{ t('imageArchive.empty') }}</p><p class="mt-2 text-sm">{{ t('imageArchive.emptyHint') }}</p></div>
      <div class="flex items-center justify-center gap-4">
        <button class="btn btn-secondary" :disabled="page <= 1 || loading" @click="changePage(-1)">{{ t('imageArchive.previous') }}</button>
        <span class="text-sm text-gray-500">{{ page }} / {{ Math.max(1, Math.ceil(total / 24)) }}</span>
        <button class="btn btn-secondary" :disabled="page * 24 >= total || loading" @click="changePage(1)">{{ t('imageArchive.next') }}</button>
      </div>
    </div>

    <BaseDialog :show="settingsOpen" :title="t('imageArchive.settings')" width="wide" @close="settingsOpen = false">
      <form class="space-y-5" @submit.prevent="saveSettings">
        <label class="flex items-center gap-3"><input v-model="draft.enabled" type="checkbox" />{{ t('imageArchive.enableCapture') }}</label>
        <p class="text-sm text-gray-500">{{ t('imageArchive.scopeHint') }}</p>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="text-sm">{{ t('imageArchive.retention') }}<input v-model.number="draft.retention_days" type="number" min="1" max="365" required class="input mt-1" /></label>
          <label class="text-sm">{{ t('imageArchive.capacityGiB') }}<input v-model.number="capacityGiB" type="number" min="1" max="2048" step="1" required class="input mt-1" /></label>
        </div>
        <fieldset><legend class="mb-2 text-sm font-medium">{{ t('imageArchive.groups') }}</legend><div class="max-h-48 space-y-2 overflow-auto rounded-lg border p-3 dark:border-dark-600"><label v-for="group in groups" :key="group.id" class="flex items-center gap-2 text-sm"><input v-model="draft.group_ids" type="checkbox" :value="group.id" />#{{ group.id }} {{ group.name }}</label></div></fieldset>
        <fieldset><legend class="mb-2 text-sm font-medium">{{ t('imageArchive.accounts') }}</legend>
          <div class="flex gap-2"><input v-model.trim="accountSearch" class="input" :placeholder="t('imageArchive.searchAccounts')" @keydown.enter.prevent="findAccounts" /><button class="btn btn-secondary" type="button" :disabled="findingAccounts" @click="findAccounts">{{ t('common.search') }}</button></div>
          <p class="my-2 break-words text-xs text-gray-500">{{ t('imageArchive.selectedIDs') }} {{ draft.account_ids.join(', ') || '—' }}</p>
          <div class="max-h-40 space-y-2 overflow-auto"><label v-for="account in accountOptions" :key="account.id" class="flex items-center gap-2 text-sm"><input v-model="draft.account_ids" type="checkbox" :value="account.id" />#{{ account.id }} {{ account.name }}</label></div>
          <button v-if="draft.account_ids.length" type="button" class="mt-2 text-xs text-primary-600" @click="draft.account_ids = []">{{ t('imageArchive.clearAccounts') }}</button>
        </fieldset>
        <p class="text-sm text-gray-500">{{ t('imageArchive.storageHint') }}</p>
        <p v-if="settingsError" role="alert" class="text-sm text-red-600">{{ settingsError }}</p>
        <button class="btn btn-primary" :disabled="saving">{{ saving ? t('common.loading') : t('common.save') }}</button>
      </form>
    </BaseDialog>

    <BaseDialog :show="detailOpen" :title="t('imageArchive.detail')" width="extra-wide" :close-on-escape="!deleting" :show-close-button="!deleting" @close="closeDetail">
      <p v-if="detailLoading" role="status">{{ t('common.loading') }}</p>
      <p v-else-if="detailError" role="alert" class="text-red-600">{{ detailError }}</p>
      <div v-else-if="detail" class="grid gap-6 lg:grid-cols-2">
        <div class="space-y-3">
          <ImageArchiveAsset v-if="selectedFile" :key="`${detail.id}-${selectedFile.id}`" :record-i-d="detail.id" :file="selectedFile" full />
          <p v-if="!selectedFile" class="py-10 text-center text-sm text-gray-500">{{ t('imageArchive.noImages') }}</p>
          <div class="flex flex-wrap gap-2"><button v-for="file in detail.files" :key="file.id" class="btn btn-secondary text-xs" :class="{ 'ring-2 ring-primary-500': selectedFile?.id === file.id }" @click="selectedFile = file">{{ t(`imageArchive.roles.${file.role}`) }} {{ file.id }}</button></div>
          <p v-if="selectedFile" class="break-all text-xs text-gray-500">{{ selectedFile.width }} × {{ selectedFile.height }} · {{ bytes(selectedFile.byte_size) }} · {{ selectedFile.mime_type }}</p>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary text-red-600" :disabled="!selectedFile || deleting" @click="deleteTarget = 'file'">{{ t('imageArchive.deleteImage') }}</button>
            <button type="button" class="btn btn-secondary text-red-600" :disabled="deleting" @click="deleteTarget = 'record'">{{ t('imageArchive.deleteRecord') }}</button>
          </div>
          <p v-if="selectedFile?.revised_prompt" class="whitespace-pre-wrap break-words text-sm">{{ t('imageArchive.revisedPrompt') }}: {{ selectedFile.revised_prompt }}</p>
        </div>
        <div class="space-y-4 text-sm">
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 break-all">
            <dt class="text-gray-500">{{ t('imageArchive.customer') }}</dt><dd>{{ detail.user_email }} (#{{ detail.user_id }})</dd>
            <dt class="text-gray-500">API Key</dt><dd>{{ detail.api_key_name }} (#{{ detail.api_key_id }})</dd>
            <dt class="text-gray-500">{{ t('imageArchive.group') }}</dt><dd>{{ detail.group_name }} (#{{ detail.group_id ?? '—' }})</dd>
            <dt class="text-gray-500">{{ t('imageArchive.account') }}</dt><dd>{{ detail.account_name }} (#{{ detail.account_id }})</dd>
            <dt class="text-gray-500">{{ t('imageArchive.model') }}</dt><dd>{{ detail.model }} → {{ detail.upstream_model }}</dd>
            <dt class="text-gray-500">{{ t('imageArchive.request') }}</dt><dd>{{ detail.request_id || detail.id }}</dd>
            <dt class="text-gray-500">{{ t('imageArchive.created') }}</dt><dd>{{ time(detail.created_at) }}</dd>
            <dt class="text-gray-500">{{ t('imageArchive.expires') }}</dt><dd>{{ time(detail.expires_at) }}</dd>
          </dl>
          <p v-if="detail.error_codes" class="text-amber-700 dark:text-amber-300">{{ codes(detail.error_codes) }}</p>
          <div><p class="mb-2 font-medium">{{ t('imageArchive.prompt') }}</p><p class="max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 dark:bg-dark-800">{{ detail.prompt || '—' }}</p></div>
          <div><p class="mb-2 font-medium">{{ t('imageArchive.parameters') }}</p><pre class="overflow-auto rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-800">{{ JSON.stringify(detail.parameters, null, 2) }}</pre></div>
        </div>
      </div>
    </BaseDialog>
    <BaseDialog :show="!!deleteTarget" :title="t('imageArchive.confirmDelete')" :z-index="80" :close-on-escape="!deleting" :show-close-button="!deleting" @close="deleteTarget = undefined">
      <p class="text-sm">{{ t(deleteTarget === 'file' ? 'imageArchive.deleteImageHint' : 'imageArchive.deleteRecordHint') }}</p>
      <p v-if="deleteError" role="alert" class="mt-3 text-sm text-red-600">{{ deleteError }}</p>
      <div class="mt-5 flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" :disabled="deleting" @click="deleteTarget = undefined">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-primary" :disabled="deleting" @click="confirmDelete">{{ deleting ? t('common.loading') : t('common.delete') }}</button>
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ImageArchiveAsset from '@/components/admin/ImageArchiveAsset.vue'
import { imageArchiveAPI, type ImageArchiveConfig, type ImageArchiveFile, type ImageArchiveRecord, type ImageArchiveStats } from '@/api/admin/imageArchive'
import { getAllIncludingInactive } from '@/api/admin/groups'
import { list as listAccounts } from '@/api/admin/accounts'

const { t, te } = useI18n()
const stats = ref<ImageArchiveStats>()
const records = ref<ImageArchiveRecord[]>([])
const groups = ref<{ id: number; name: string }[]>([])
const total = ref(0), page = ref(1), loading = ref(false), error = ref('')
const filters = reactive({ user: '', group: '', account: '', model: '', from: '', to: '' })
const settingsOpen = ref(false), saving = ref(false), settingsError = ref('')
const draft = reactive<ImageArchiveConfig>({ enabled: false, account_ids: [], group_ids: [], retention_days: 30, max_bytes: 20 * 2 ** 30 })
const capacityGiB = ref(20), accountSearch = ref(''), findingAccounts = ref(false)
const accountOptions = ref<{ id: number; name: string }[]>([])
const detailOpen = ref(false), detailLoading = ref(false), detailError = ref('')
const detail = ref<ImageArchiveRecord>(), selectedFile = ref<ImageArchiveFile>()
const deleteTarget = ref<'file' | 'record'>(), deleting = ref(false), deleteError = ref('')
watch(deleteTarget, () => { deleteError.value = '' })
async function confirmDelete() {
  if (!detail.value || !deleteTarget.value || deleting.value) return
  const id = detail.value.id
  const fileID = deleteTarget.value === 'file' ? selectedFile.value?.id : undefined
  if (deleteTarget.value === 'file' && fileID === undefined) return
  deleting.value = true; deleteError.value = ''
  try {
    await imageArchiveAPI.remove(id, fileID)
    deleteTarget.value = undefined
    if (fileID === undefined) { closeDetail(); if (records.value.length === 1 && page.value > 1) page.value-- }
    else {
      detail.value.files = detail.value.files.filter(file => file.id !== fileID)
      selectedFile.value = cover(detail.value)
    }
    await refresh()
  } catch (err) { deleteError.value = message(err) }
  finally { deleting.value = false }
}
let listController: AbortController | undefined
let detailController: AbortController | undefined
let listVersion = 0

function bytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 2 ** 20) return `${(value / 1024).toFixed(1)} KiB`
  if (value < 2 ** 30) return `${(value / 2 ** 20).toFixed(1)} MiB`
  return `${(value / 2 ** 30).toFixed(2)} GiB`
}
function time(value: string) { return new Date(value).toLocaleString() }
function codes(value: string) { return value.split(',').map(code => te(`imageArchive.errors.${code}`) ? t(`imageArchive.errors.${code}`) : code).join(' · ') }
function message(err: unknown) { return err instanceof Error ? err.message : (err as { message?: string })?.message || t('imageArchive.loadFailed') }
function cover(record: ImageArchiveRecord) { return record.files.find(file => file.role === 'result') || record.files[0] }
async function loadRecords() {
  listController?.abort()
  const version = ++listVersion
  listController = new AbortController()
  loading.value = true; error.value = ''
  try {
    const data = await imageArchiveAPI.list({ page: page.value, page_size: 24,
      user_id: Number(filters.user) || undefined, account_id: Number(filters.account) || undefined,
      group_id: Number(filters.group) || undefined, model: filters.model || undefined,
      from: filters.from ? new Date(filters.from).toISOString() : undefined,
      to: filters.to ? new Date(filters.to).toISOString() : undefined,
    }, listController.signal)
    if (version === listVersion) { records.value = data.items; total.value = data.total }
  } catch (err) { if (version === listVersion && !listController.signal.aborted) error.value = message(err) }
  finally { if (version === listVersion) loading.value = false }
}
async function refresh() {
  await Promise.all([loadRecords(), imageArchiveAPI.stats().then(value => { stats.value = value }).catch(err => { error.value = message(err) })])
}
function search() { page.value = 1; void loadRecords() }
function changePage(delta: number) { page.value += delta; void loadRecords() }
function openSettings() {
  if (!stats.value) return
  Object.assign(draft, { ...stats.value.config, account_ids: [...stats.value.config.account_ids], group_ids: [...stats.value.config.group_ids] })
  capacityGiB.value = draft.max_bytes / 2 ** 30
  settingsError.value = ''; settingsOpen.value = true
}
async function findAccounts() {
  findingAccounts.value = true; settingsError.value = ''
  try { accountOptions.value = (await listAccounts(1, 20, { search: accountSearch.value })).items }
  catch (err) { settingsError.value = message(err) }
  finally { findingAccounts.value = false }
}
async function saveSettings() {
  if (draft.enabled && !draft.account_ids.length && !draft.group_ids.length) { settingsError.value = t('imageArchive.chooseScope'); return }
  saving.value = true; settingsError.value = ''
  try {
    stats.value = await imageArchiveAPI.configure({ ...draft, max_bytes: capacityGiB.value * 2 ** 30 })
    settingsOpen.value = false
  } catch (err) { settingsError.value = message(err) }
  finally { saving.value = false }
}
async function openRecord(id: string) {
  closeDetail(); detailOpen.value = true; detailLoading.value = true; detailError.value = ''
  const controller = new AbortController(); detailController = controller
  try {
    const value = await imageArchiveAPI.get(id, controller.signal)
    if (!controller.signal.aborted) { detail.value = value; selectedFile.value = cover(value) }
  } catch (err) { if (!controller.signal.aborted) detailError.value = message(err) }
  finally { if (!controller.signal.aborted) detailLoading.value = false }
}
function closeDetail() { deleteTarget.value = undefined; deleteError.value = ''; detailController?.abort(); detailOpen.value = false; detail.value = undefined; selectedFile.value = undefined }
onMounted(async () => {
  await Promise.all([refresh(), getAllIncludingInactive().then(value => { groups.value = value }).catch(err => { error.value = message(err) })])
})
onBeforeUnmount(() => { ++listVersion; listController?.abort(); closeDetail() })
</script>
