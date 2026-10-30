<template>
  <AppLayout>
    <div class="space-y-6">
      <header class="flex items-start justify-between gap-4">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-gray-100">{{ t('imageArchive.customerTitle') }}</h1>
          <p class="mt-2 max-w-2xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('imageArchive.customerHint') }}</p>
        </div>
        <button class="btn btn-secondary shrink-0 gap-2" :disabled="loading" @click="load(page)">
          <Icon name="refresh" size="sm" aria-hidden="true" />{{ t('common.refresh') }}
        </button>
      </header>
      <div v-if="error" class="history-empty">
        <Icon name="exclamationCircle" size="lg" class="mb-3 text-gray-400" aria-hidden="true" />
        <p role="alert" class="text-sm text-gray-600 dark:text-gray-300">{{ t('imageArchive.loadFailed') }}</p>
        <button class="btn btn-secondary mt-4" @click="load(page)">{{ t('imageArchive.retry') }}</button>
      </div>
      <section v-else-if="loading" role="status" :aria-label="t('common.loading')" class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
        <span class="sr-only">{{ t('common.loading') }}</span>
        <div v-for="n in 6" :key="n" aria-hidden="true" class="overflow-hidden rounded-2xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
          <div class="aspect-[4/3] bg-gray-100 dark:bg-dark-800 motion-safe:animate-pulse" />
          <div class="space-y-3 p-4"><div class="h-4 w-2/3 rounded bg-gray-100 dark:bg-dark-800" /><div class="h-3 w-1/2 rounded bg-gray-100 dark:bg-dark-800" /><div class="h-9 rounded bg-gray-50 dark:bg-dark-800" /></div>
        </div>
      </section>
      <div v-else-if="!items.length" class="history-empty">
        <Icon name="image" size="xl" class="mb-4 text-gray-400" aria-hidden="true" />
        <p class="max-w-sm text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('imageArchive.customerEmpty') }}</p>
      </div>
      <template v-else>
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('imageArchive.total', { n: total }) }}</p>
        <section class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3" :aria-label="t('imageArchive.customerTitle')">
          <article v-for="item in items" :key="item.id" class="flex min-w-0 flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
            <div class="relative aspect-[4/3] overflow-hidden border-b border-gray-100 bg-gray-100 dark:border-dark-700 dark:bg-dark-800">
              <button v-if="item.files[0]" type="button" class="history-preview block h-full w-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500" :aria-label="t('imageArchive.detail')" @click="open(item)">
                <ImageArchiveAsset :record-i-d="item.id" :file="{ ...item.files[0], role: 'result' }" :load-file="imageHistoryAPI.file" />
              </button>
              <div v-else class="flex h-full flex-col items-center justify-center gap-3 p-6 text-gray-400 dark:text-gray-500">
                <Icon :name="item.status === 'failed' ? 'exclamationCircle' : 'image'" size="xl" aria-hidden="true" />
                <p class="text-sm">{{ t('imageArchive.noImages') }}</p>
              </div>
              <span v-if="item.files.length > 1" class="pointer-events-none absolute bottom-3 right-3 rounded-md bg-gray-900/75 px-2 py-1 text-xs font-medium text-white">{{ t('imageArchive.savedImages', { n: item.files.length }) }}</span>
            </div>
            <div class="flex flex-1 flex-col gap-4 p-4 sm:p-5">
              <div class="min-w-0">
                <h2 class="truncate text-sm font-semibold text-gray-900 dark:text-gray-100" :title="item.model">{{ item.model }}</h2>
                <time :datetime="item.created_at" class="mt-1 block text-xs tabular-nums text-gray-500 dark:text-gray-400">{{ date(item.created_at) }}</time>
              </div>
              <div class="flex flex-wrap items-center gap-2">
                <span class="history-status" :class="statusClass(item.status)"><Icon :name="statusIcon(item.status)" size="xs" aria-hidden="true" />{{ t(`imageArchive.generation.${item.status}`) }}</span>
                <span v-if="item.delivery_status === 'interrupted'" class="text-xs text-amber-700 dark:text-amber-400">{{ t('imageArchive.transferInterrupted') }}</span>
              </div>
              <div class="mt-auto flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1 border-t border-gray-100 pt-3 text-xs dark:border-dark-700">
                <span class="text-gray-500 dark:text-gray-400">{{ t('imageArchive.customerBilling') }}</span>
                <span class="font-medium tabular-nums text-gray-800 dark:text-gray-200">{{ billing(item) }}</span>
              </div>
              <button type="button" class="btn btn-secondary w-full justify-between gap-2 text-sm" @click="open(item)">{{ t('imageArchive.detail') }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></button>
            </div>
          </article>
        </section>
      </template>
      <nav v-if="!error && total > 0" class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 pt-5 dark:border-dark-700" :aria-label="t('imageArchive.pagination')">
        <span class="order-first w-full text-center text-xs tabular-nums text-gray-500 dark:text-gray-400 sm:order-none sm:w-auto sm:text-left">{{ t('imageArchive.pageOf', { page, pages: pageCount }) }}</span>
        <div class="flex w-full justify-between gap-3 sm:w-auto">
          <button class="btn btn-secondary gap-1.5" :disabled="loading || page <= 1" @click="load(page - 1)"><Icon name="chevronLeft" size="sm" aria-hidden="true" />{{ t('imageArchive.previous') }}</button>
          <button class="btn btn-secondary gap-1.5" :disabled="loading || page >= pageCount" @click="load(page + 1)">{{ t('imageArchive.next') }}<Icon name="chevronRight" size="sm" aria-hidden="true" /></button>
        </div>
      </nav>
    </div>
    <BaseDialog :show="!!selected" :title="t('imageArchive.detail')" width="wide" @close="selected = null">
      <div v-if="selected" class="grid min-w-0 gap-6 md:grid-cols-[minmax(0,1fr)_16rem]">
        <div class="min-w-0 space-y-3">
          <template v-if="selectedFile">
            <div class="overflow-hidden rounded-xl border border-gray-200 dark:border-dark-700">
              <ImageArchiveAsset :key="selectedFile.id" :record-i-d="selected.id" :file="{ ...selectedFile, role: 'result' }" full close-on-backdrop :load-file="imageHistoryAPI.file" />
            </div>
            <div v-if="selected.files.length > 1" class="flex flex-wrap gap-2" role="group" :aria-label="t('imageArchive.selectImage')">
              <button v-for="(file, index) in selected.files" :key="file.id" type="button" class="btn btn-sm"
                :class="selectedFile.id === file.id ? 'btn-primary' : 'btn-secondary'"
                :aria-label="t('imageArchive.imageNumber', { n: index + 1 })" :aria-pressed="selectedFile.id === file.id" @click="fileIndex = index">{{ index + 1 }}</button>
            </div>
            <p v-if="selectedFile.preview_only" class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('imageArchive.previewOnlyFile') }}</p>
          </template>
          <div v-else class="history-empty min-h-64"><Icon name="image" size="xl" class="mb-3 text-gray-400" aria-hidden="true" /><p class="text-sm text-gray-500 dark:text-gray-400">{{ t('imageArchive.noImages') }}</p></div>
        </div>
        <div class="min-w-0 space-y-5">
          <div class="space-y-3">
            <span class="history-status" :class="statusClass(selected.status)"><Icon :name="statusIcon(selected.status)" size="xs" aria-hidden="true" />{{ t(`imageArchive.generation.${selected.status}`) }}</span>
            <p v-if="selected.delivery_status === 'interrupted'" class="text-xs leading-5 text-amber-700 dark:text-amber-400">{{ t('imageArchive.transferInterrupted') }}</p>
            <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('imageArchive.customerBilling') }}</p>
              <p class="mt-2 break-words text-sm font-semibold tabular-nums text-gray-900 dark:text-gray-100">{{ billing(selected) }}</p>
            </div>
          </div>
          <dl class="space-y-4 text-xs">
            <div><dt class="text-gray-500 dark:text-gray-400">{{ t('imageArchive.model') }}</dt><dd class="mt-1.5 break-words font-medium text-gray-800 dark:text-gray-200">{{ selected.model }}</dd></div>
            <div><dt class="text-gray-500 dark:text-gray-400">{{ t('imageArchive.request') }}</dt><dd class="mt-1.5 break-all font-mono text-gray-600 dark:text-gray-300">{{ selected.request_id }}</dd></div>
            <div><dt class="text-gray-500 dark:text-gray-400">{{ t('imageArchive.created') }}</dt><dd class="mt-1.5 tabular-nums"><time :datetime="selected.created_at">{{ date(selected.created_at) }}</time></dd></div>
            <div><dt class="text-gray-500 dark:text-gray-400">{{ t('imageArchive.expires') }}</dt><dd class="mt-1.5 tabular-nums"><time :datetime="selected.expires_at">{{ date(selected.expires_at) }}</time></dd></div>
          </dl>
          <template v-if="selectedFile">
            <button class="btn btn-primary w-full gap-2" :disabled="downloading" @click="download"><Icon name="download" size="sm" aria-hidden="true" />{{ t(downloading ? 'common.loading' : 'imageArchive.downloadSaved') }}</button>
            <p v-if="downloadFailed" role="alert" class="text-xs text-red-600 dark:text-red-400">{{ t('imageArchive.imageFailed') }}</p>
          </template>
        </div>
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { saveAs } from 'file-saver'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ImageArchiveAsset from '@/components/admin/ImageArchiveAsset.vue'
import Icon from '@/components/icons/Icon.vue'
import { imageHistoryAPI, type ImageHistoryRecord } from '@/api/imageHistory'

const { t, locale } = useI18n()
const items = ref<ImageHistoryRecord[]>([])
const selected = ref<ImageHistoryRecord | null>(null)
const page = ref(1), total = ref(0), fileIndex = ref(0)
const pageSize = ref(20)
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))
const loading = ref(false), error = ref(false), downloading = ref(false), downloadFailed = ref(false)
const selectedFile = computed(() => selected.value?.files[fileIndex.value])
let controller: AbortController | undefined, downloadController: AbortController | undefined, generation = 0
function date(value: string) { return new Date(value).toLocaleString(locale.value) }
function statusIcon(status: ImageHistoryRecord['status']) {
  return status === 'succeeded' ? 'checkCircle' : status === 'failed' ? 'xCircle' : 'clock'
}
function statusClass(status: ImageHistoryRecord['status']) {
  if (status === 'succeeded') return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400'
  if (status === 'failed') return 'bg-red-50 text-red-700 dark:bg-red-500/10 dark:text-red-400'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-gray-300'
}
function billing(item: ImageHistoryRecord) {
  const receipt = item.billing
  if (receipt.status === 'charged' && receipt.amount !== undefined) {
    return t('imageArchive.chargedAmount', { amount: receipt.amount.toFixed(6) })
  }
  return t(`imageArchive.receipt.${receipt.status}`)
}
async function load(nextPage: number) {
  controller?.abort()
  const current = ++generation
  controller = new AbortController()
  loading.value = true; error.value = false; selected.value = null
  // Unmount old thumbnails immediately so in-flight blob reads are cancelled.
  items.value = []
  try {
    const result = await imageHistoryAPI.list(nextPage, controller.signal)
    if (current !== generation) return
    items.value = result.items; total.value = result.total; page.value = result.page
    pageSize.value = result.page_size > 0 ? result.page_size : 20
  } catch {
    if (current === generation && !controller.signal.aborted) error.value = true
  } finally {
    if (current === generation) loading.value = false
  }
}
function open(item: ImageHistoryRecord) { selected.value = item; fileIndex.value = 0; downloadFailed.value = false }
async function download() {
  const record = selected.value, file = selectedFile.value
  if (!record || !file || downloading.value) return
  downloadController = new AbortController()
  downloading.value = true; downloadFailed.value = false
  try {
    const blob = await imageHistoryAPI.file(record.id, file.id, false, downloadController.signal)
    if (!downloadController.signal.aborted) {
      const extensions: Record<string, string> = { 'image/jpeg': 'jpg', 'image/webp': 'webp', 'image/gif': 'gif', 'image/png': 'png' }
      const ext = extensions[file.mime_type] ?? 'png'
      saveAs(blob, `image-${record.id}-${file.id}.${ext}`)
    }
  } catch {
    if (!downloadController.signal.aborted) downloadFailed.value = true
  } finally { downloading.value = false }
}
onMounted(() => load(1))
onBeforeUnmount(() => { ++generation; controller?.abort(); downloadController?.abort() })
</script>

<style scoped>
.history-empty {
  @apply flex flex-col items-center justify-center rounded-2xl border border-dashed border-gray-200 bg-white px-6 py-16 text-center dark:border-dark-700 dark:bg-dark-900;
}
.history-status {
  @apply inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium;
}
.history-preview :deep(img) {
  max-height: 100%;
}
</style>
