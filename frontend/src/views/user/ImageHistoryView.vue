<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex items-start justify-between gap-4">
        <div>
          <h1 class="text-xl font-semibold">{{ t('imageArchive.customerTitle') }}</h1>
          <p class="mt-1 text-sm text-gray-500">{{ t('imageArchive.customerHint') }}</p>
        </div>
        <button class="btn btn-secondary" :disabled="loading" @click="load(page)">{{ t('common.refresh') }}</button>
      </header>
      <p v-if="error" role="alert" class="text-red-600">{{ t('imageArchive.loadFailed') }}</p>
      <p v-if="loading" role="status">{{ t('common.loading') }}</p>
      <p v-else-if="!error && !items.length" class="py-12 text-center text-gray-500">{{ t('imageArchive.customerEmpty') }}</p>
      <section v-else class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <article v-for="item in items" :key="item.id" class="overflow-hidden rounded-xl border border-gray-200 dark:border-dark-600">
          <button v-if="item.files[0]" type="button" class="block h-48 w-full" :aria-label="t('imageArchive.detail')" @click="open(item)">
            <ImageArchiveAsset :record-i-d="item.id" :file="{ ...item.files[0], role: 'result' }" :load-file="imageHistoryAPI.file" />
          </button>
          <div class="space-y-2 p-4 text-sm">
            <p class="font-medium">{{ item.model }}</p>
            <p class="text-gray-500">{{ date(item.created_at) }}</p>
            <div class="flex flex-wrap gap-x-4 gap-y-1">
              <span>{{ t(`imageArchive.generation.${item.status}`) }}</span>
              <span>{{ billing(item) }}</span>
            </div>
            <p v-if="item.delivery_status === 'interrupted'" class="text-amber-600">{{ t('imageArchive.transferInterrupted') }}</p>
            <button type="button" class="btn btn-secondary btn-sm" @click="open(item)">{{ t('imageArchive.detail') }}</button>
          </div>
        </article>
      </section>
      <nav class="flex items-center justify-between gap-3" :aria-label="t('imageArchive.customerTitle')">
        <button class="btn btn-secondary" :disabled="loading || page <= 1" @click="load(page - 1)">{{ t('imageArchive.previous') }}</button>
        <span class="text-sm">{{ t('imageArchive.total', { n: total }) }} · {{ page }}</span>
        <button class="btn btn-secondary" :disabled="loading || page * 20 >= total" @click="load(page + 1)">{{ t('imageArchive.next') }}</button>
      </nav>
    </div>
    <BaseDialog :show="!!selected" :title="t('imageArchive.detail')" @close="selected = null">
      <div v-if="selected" class="space-y-4">
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 break-all text-sm">
          <dt>{{ t('imageArchive.request') }}</dt><dd>{{ selected.request_id }}</dd>
          <dt>{{ t('imageArchive.model') }}</dt><dd>{{ selected.model }}</dd>
          <dt>{{ t('imageArchive.created') }}</dt><dd>{{ date(selected.created_at) }}</dd>
          <dt>{{ t('imageArchive.expires') }}</dt><dd>{{ date(selected.expires_at) }}</dd>
          <dt>{{ t('imageArchive.customerBilling') }}</dt><dd>{{ billing(selected) }}</dd>
        </dl>
        <p>{{ t(`imageArchive.generation.${selected.status}`) }}</p>
        <p v-if="selected.delivery_status === 'interrupted'">{{ t('imageArchive.transferInterrupted') }}</p>
        <template v-if="selected.files.length && selectedFile">
          <div v-if="selected.files.length > 1" class="flex flex-wrap gap-2">
            <button v-for="(file, index) in selected.files" :key="file.id" type="button" class="btn btn-secondary btn-sm"
              :aria-pressed="selectedFile.id === file.id" @click="fileIndex = index">{{ index + 1 }}</button>
          </div>
          <ImageArchiveAsset :record-i-d="selected.id" :file="{ ...selectedFile, role: 'result' }" full close-on-backdrop :load-file="imageHistoryAPI.file" />
          <p v-if="selectedFile.preview_only" class="text-sm text-gray-500">{{ t('imageArchive.previewOnlyFile') }}</p>
          <button class="btn btn-primary" :disabled="downloading" @click="download">{{ t('imageArchive.downloadSaved') }}</button>
          <p v-if="downloadFailed" role="alert">{{ t('imageArchive.imageFailed') }}</p>
        </template>
        <p v-else class="text-gray-500">{{ t('imageArchive.noImages') }}</p>
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
import { imageHistoryAPI, type ImageHistoryRecord } from '@/api/imageHistory'

const { t, locale } = useI18n()
const items = ref<ImageHistoryRecord[]>([])
const selected = ref<ImageHistoryRecord | null>(null)
const page = ref(1), total = ref(0), fileIndex = ref(0)
const loading = ref(false), error = ref(false), downloading = ref(false), downloadFailed = ref(false)
const selectedFile = computed(() => selected.value?.files[fileIndex.value])
let controller: AbortController | undefined, downloadController: AbortController | undefined, generation = 0
function date(value: string) { return new Date(value).toLocaleString(locale.value) }
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
