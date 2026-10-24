<template>
  <div
    class="flex min-h-32 items-center justify-center bg-gray-100 dark:bg-dark-800"
    :class="full ? 'aspect-square max-h-[65vh] w-full' : 'h-full'"
  >
    <img
      v-if="src" :src="src" :alt="t(`imageArchive.roles.${file.role}`)"
      class="h-full max-h-[65vh] w-full object-contain"
      :class="{ 'cursor-zoom-in': full }" :role="full ? 'button' : undefined"
      :tabindex="full ? 0 : undefined" :aria-label="full ? t('imageArchive.openViewer') : undefined"
      @click="openViewer" @keydown.enter.prevent="openViewer" @keydown.space.prevent="openViewer"
    />
    <span v-else class="px-4 text-center text-sm text-gray-500" role="status">
      {{ failed ? t('imageArchive.imageFailed') : (!full && !file.thumbnail ? t('imageArchive.noThumbnail') : t('common.loading')) }}
    </span>
  </div>
  <BaseDialog v-if="full" :show="viewerOpen" :title="t('imageArchive.imageViewer')" :z-index="70"
    :show-close-button="false" content-class="image-archive-lightbox" @close="viewerOpen = false">
    <div ref="viewport" class="absolute inset-0 flex touch-none select-none items-center justify-center overflow-hidden"
      :class="scale > 1 ? (dragging ? 'cursor-grabbing' : 'cursor-grab') : ''"
      data-testid="archive-viewport"
      @wheel.prevent="wheel" @pointerdown="startPointer" @pointermove="movePointer"
      @pointerup="endPointer" @pointercancel="endPointer" @lostpointercapture="endPointer"
      @click="clickBackdrop" @dblclick="resetView">
      <img ref="viewerImage" v-if="viewerOpen && src" :src="src" :alt="t(`imageArchive.roles.${file.role}`)" draggable="false"
        class="pointer-events-none h-[calc(100%_-_9rem)] w-[calc(100%_-_2rem)] object-contain"
        :style="{ transform: `translate(${offset.x}px, ${offset.y}px) scale(${scale})` }" />
    </div>
    <button type="button" class="archive-viewer-close archive-viewer-control" :aria-label="t('imageArchive.closeViewer')" @click="viewerOpen = false">×</button>
    <div class="archive-viewer-toolbar">
      <button type="button" class="archive-viewer-control" :aria-label="t('imageArchive.zoomOut')" :disabled="scale <= 0.25" @click="zoom(scale / 1.25)">−</button>
      <span class="min-w-14 text-center text-sm tabular-nums" role="status">{{ Math.round(scale * 100) }}%</span>
      <button type="button" class="archive-viewer-control" :aria-label="t('imageArchive.zoomIn')" :disabled="scale >= 8" @click="zoom(scale * 1.25)">+</button>
      <button type="button" class="archive-viewer-control px-3 text-sm" @click="resetView">{{ t('imageArchive.resetView') }}</button>
    </div>
    <p class="archive-viewer-hint">{{ t('imageArchive.viewerHint') }}</p>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onBeforeUnmount, reactive, ref, watch } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useI18n } from 'vue-i18n'
import { imageArchiveAPI, type ImageArchiveFile } from '@/api/admin/imageArchive'

const props = defineProps<{ recordID: string; file: ImageArchiveFile; full?: boolean; closeOnBackdrop?: boolean }>()
const { t } = useI18n()
const src = ref('')
const failed = ref(false)
const viewerOpen = ref(false), scale = ref(1), dragging = ref(false)
const viewport = ref<HTMLElement>()
const viewerImage = ref<HTMLImageElement>()
const offset = reactive({ x: 0, y: 0 })
type Point = { x: number; y: number }
const pointers = new Map<number, Point>()
let dragOrigin: Point = { x: 0, y: 0 }
let backdropPress: Point | undefined
let backdropMoved = false
function insideImage(point: Point) {
  const img = viewerImage.value
  if (!img?.naturalWidth || !img.naturalHeight) return true
  const rect = img.getBoundingClientRect()
  // object-contain leaves letterboxing inside the element's CSS box. Test the
  // actual rendered image, including its current scale and translation.
  const ratio = Math.min(rect.width / img.naturalWidth, rect.height / img.naturalHeight)
  const width = img.naturalWidth * ratio, height = img.naturalHeight * ratio
  const left = rect.left + (rect.width - width) / 2, top = rect.top + (rect.height - height) / 2
  return point.x >= left && point.x <= left + width && point.y >= top && point.y <= top + height
}
function clickBackdrop(event: MouseEvent) {
  const press = backdropPress
  backdropPress = undefined
  if (!props.closeOnBackdrop || !press || backdropMoved) return
  const point = { x: event.clientX, y: event.clientY }
  if (Math.hypot(point.x - press.x, point.y - press.y) > 5) return
  if (!insideImage(press) && !insideImage(point)) viewerOpen.value = false
}
let pinch: { distance: number; scale: number; anchor: Point } | undefined
function center(): Point {
  const rect = viewport.value?.getBoundingClientRect()
  return rect ? { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 } : { x: 0, y: 0 }
}
function clearPointers() {
  const ids = [...pointers.keys()]
  pointers.clear(); pinch = undefined; dragging.value = false
  backdropPress = undefined; backdropMoved = false
  for (const id of ids) {
    if (viewport.value?.hasPointerCapture(id)) viewport.value.releasePointerCapture(id)
  }
}
function resetView() { clearPointers(); scale.value = 1; offset.x = 0; offset.y = 0 }
function openViewer() { if (props.full && src.value) { resetView(); viewerOpen.value = true } }
function zoom(value: number, origin = center()) {
  const next = Math.min(8, Math.max(0.25, value))
  const c = center(), ratio = next / scale.value
  offset.x = origin.x - c.x - (origin.x - c.x - offset.x) * ratio
  offset.y = origin.y - c.y - (origin.y - c.y - offset.y) * ratio
  scale.value = next
  if (next <= 1) { offset.x = 0; offset.y = 0 }
}
function wheel(event: WheelEvent) {
  if (event.deltaY) {
    backdropMoved = true
    zoom(scale.value * (event.deltaY < 0 ? 1.1 : 1 / 1.1), { x: event.clientX, y: event.clientY })
    rebaseGesture()
  }
}
function rebaseGesture() {
  const points = [...pointers.values()]
  dragging.value = points.length > 0 && (scale.value > 1 || points.length > 1)
  pinch = undefined
  if (points.length >= 2) {
    const [a, b] = points, c = center()
    const mid = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 }
    pinch = {
      distance: Math.hypot(a.x - b.x, a.y - b.y),
      scale: scale.value,
      anchor: { x: (mid.x - c.x - offset.x) / scale.value, y: (mid.y - c.y - offset.y) / scale.value },
    }
  } else if (points.length === 1) {
    dragOrigin = { x: points[0].x - offset.x, y: points[0].y - offset.y }
  }
}
function startPointer(event: PointerEvent) {
  if (event.pointerType === 'mouse' && event.button !== 0) return
  if (pointers.size >= 2) return
  if (pointers.size === 0) {
    backdropPress = { x: event.clientX, y: event.clientY }
    backdropMoved = false
  } else {
    backdropMoved = true
  }
  pointers.set(event.pointerId, { x: event.clientX, y: event.clientY })
  viewport.value?.setPointerCapture(event.pointerId)
  rebaseGesture()
}
function movePointer(event: PointerEvent) {
  if (!pointers.has(event.pointerId)) return
  if (backdropPress && Math.hypot(event.clientX - backdropPress.x, event.clientY - backdropPress.y) > 5) backdropMoved = true
  pointers.set(event.pointerId, { x: event.clientX, y: event.clientY })
  if (pointers.size >= 2 && pinch && pinch.distance > 0) {
    const [a, b] = [...pointers.values()], c = center()
    const next = Math.min(8, Math.max(0.25, pinch.scale * Math.hypot(a.x - b.x, a.y - b.y) / pinch.distance))
    scale.value = next
    offset.x = (a.x + b.x) / 2 - c.x - pinch.anchor.x * next
    offset.y = (a.y + b.y) / 2 - c.y - pinch.anchor.y * next
    if (next <= 1) { offset.x = 0; offset.y = 0 }
  } else if (scale.value > 1) {
    offset.x = event.clientX - dragOrigin.x; offset.y = event.clientY - dragOrigin.y
  }
}
function endPointer(event: PointerEvent) {
  if (!pointers.delete(event.pointerId)) return
  if (event.type !== 'pointerup') backdropPress = undefined
  if (viewport.value?.hasPointerCapture(event.pointerId)) viewport.value.releasePointerCapture(event.pointerId)
  rebaseGesture()
}
watch(viewerOpen, open => { if (!open) clearPointers() })
let controller: AbortController | undefined
let generation = 0
function release() {
  viewerOpen.value = false
  resetView()
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

<style>
/* Scoped by a unique dialog class so all other BaseDialog panels keep their layout. */
.modal-content.image-archive-lightbox {
  position: fixed;
  inset: 0;
  width: 100%;
  max-width: none;
  height: 100dvh;
  max-height: none;
  margin: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}
.image-archive-lightbox > .modal-header {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  padding: 0;
  border: 0;
}
.image-archive-lightbox > .modal-body { padding: 0; overflow: hidden; }
.archive-viewer-control {
  display: inline-flex;
  min-width: 44px;
  min-height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 9999px;
  background: transparent;
  color: white;
  font-size: 1.25rem;
  touch-action: manipulation;
}
.archive-viewer-control:hover { background: rgb(255 255 255 / 15%); }
.archive-viewer-control:focus-visible { outline: 2px solid white; outline-offset: 2px; }
.archive-viewer-control:disabled { opacity: 0.35; }
.archive-viewer-close {
  position: absolute;
  top: max(12px, env(safe-area-inset-top));
  right: max(12px, env(safe-area-inset-right));
  background: rgb(0 0 0 / 45%);
  font-size: 1.75rem;
}
.archive-viewer-toolbar {
  position: absolute;
  bottom: calc(max(12px, env(safe-area-inset-bottom)) + 36px);
  left: 50%;
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  border-radius: 9999px;
  background: rgb(0 0 0 / 55%);
  color: white;
  white-space: nowrap;
  transform: translateX(-50%);
}
.archive-viewer-hint {
  position: absolute;
  right: 16px;
  bottom: max(12px, env(safe-area-inset-bottom));
  left: 16px;
  color: white;
  text-align: center;
  font-size: 12px;
  text-shadow: 0 1px 4px black;
  pointer-events: none;
}
</style>
