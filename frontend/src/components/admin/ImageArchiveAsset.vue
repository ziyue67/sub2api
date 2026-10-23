<template>
  <div
    class="flex min-h-32 items-center justify-center bg-gray-100 dark:bg-dark-800"
    :class="full ? 'aspect-square max-h-[65vh] w-full' : 'h-full'"
  >
    <img v-if="src" :src="src" :alt="t(`imageArchive.roles.${file.role}`)" class="h-full max-h-[65vh] w-full object-contain" />
    <span v-else class="px-4 text-center text-sm text-gray-500" role="status">
      {{ failed ? t('imageArchive.imageFailed') : (!full && !file.thumbnail ? t('imageArchive.noThumbnail') : t('common.loading')) }}
    </span>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { imageArchiveAPI, type ImageArchiveFile } from '@/api/admin/imageArchive'

const props = defineProps<{ recordID: string; file: ImageArchiveFile; full?: boolean }>()
const { t } = useI18n()
const src = ref('')
const failed = ref(false)
let controller: AbortController | undefined
let generation = 0
function release() {
  controller?.abort()
  if (src.value) URL.revokeObjectURL(src.value)
  src.value = ''
}
watch(() => [props.recordID, props.file.id, props.full], async () => {
  release()
  const request = ++generation
  failed.value = false
  if (!props.full && !props.file.thumbnail) return
  controller = new AbortController()
  try {
    const blob = await imageArchiveAPI.file(props.recordID, props.file.id, !props.full, controller.signal)
    if (request === generation) src.value = URL.createObjectURL(blob)
  } catch {
    if (request === generation && !controller.signal.aborted) failed.value = true
  }
}, { immediate: true })
onBeforeUnmount(() => { ++generation; release() })
</script>
