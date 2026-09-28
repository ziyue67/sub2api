<template>
  <div>
    <!-- The padding, cancelled by the negative margin, keeps the cards' hover lift and focus ring inside the clip. -->
    <div
      :id="scrollerId"
      ref="scrollerRef"
      class="scrollbar-hide -m-2 flex gap-5 overflow-x-auto overscroll-x-contain p-2"
      data-testid="pelican-showcase-row"
      @scroll.passive="sync"
    >
      <slot />
    </div>

    <!-- App scrollbars stay transparent until hovered, so the row hides its native one and shows this slider instead. -->
    <div
      v-show="overflowing"
      role="scrollbar"
      tabindex="0"
      aria-orientation="horizontal"
      aria-valuemin="0"
      aria-valuemax="100"
      :aria-valuenow="Math.round(progress * 100)"
      :aria-controls="scrollerId"
      :aria-label="label"
      class="group/rail mt-3 flex h-4 cursor-pointer touch-none select-none items-center rounded-full focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500"
      data-testid="pelican-showcase-slider"
      @pointerdown="startDrag"
      @pointermove="moveDrag"
      @pointerup="endDrag"
      @pointercancel="endDrag"
      @lostpointercapture="endDrag"
      @keydown="onKeydown"
    >
      <div
        ref="trackRef"
        class="relative w-full rounded-full bg-gray-200 transition-[height] group-hover/rail:h-2 dark:bg-dark-700"
        :class="dragging ? 'h-2' : 'h-1.5'"
      >
        <div
          ref="thumbRef"
          class="absolute inset-y-0 min-w-10 rounded-full transition-colors"
          :class="dragging ? 'bg-primary-500' : 'bg-gray-400 group-hover/rail:bg-gray-500 dark:bg-dark-500 dark:group-hover/rail:bg-dark-400'"
          :style="thumbStyle"
          data-testid="pelican-showcase-slider-thumb"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId } from 'vue'

defineProps<{
  /** Accessible name of the slider. */
  label: string
}>()

const scrollerId = `pelican-row-${useId()}`
const scrollerRef = ref<HTMLElement | null>(null)
const trackRef = ref<HTMLElement | null>(null)
const thumbRef = ref<HTMLElement | null>(null)
/** Visible share of the row, 1 when everything fits. */
const visibleRatio = ref(1)
/** Scroll position from 0 (start) to 1 (end). */
const progress = ref(0)
const dragging = ref(false)
let drag: { pointerId: number; startX: number; startScroll: number; scale: number } | null = null
let resizeObserver: ResizeObserver | undefined
let mutationObserver: MutationObserver | undefined

const overflowing = computed(() => visibleRatio.value < 1)
// left + translateX(-same%) puts the thumb at progress × (track − thumb), whatever min-width made the thumb.
const thumbStyle = computed(() => ({
  width: `${visibleRatio.value * 100}%`,
  left: `${progress.value * 100}%`,
  transform: `translateX(-${progress.value * 100}%)`,
}))

const KEY_TARGETS: Record<string, (row: HTMLElement) => number> = {
  ArrowLeft: (row) => row.scrollLeft - cardStep(row),
  ArrowRight: (row) => row.scrollLeft + cardStep(row),
  PageUp: (row) => row.scrollLeft - row.clientWidth,
  PageDown: (row) => row.scrollLeft + row.clientWidth,
  Home: () => 0,
  End: (row) => row.scrollWidth,
}

function sync() {
  const row = scrollerRef.value
  if (!row) return
  const max = row.scrollWidth - row.clientWidth
  // Rounded scroll sizes can differ by a pixel even when every card fits.
  if (max <= 1) {
    visibleRatio.value = 1
    progress.value = 0
    return
  }
  visibleRatio.value = row.clientWidth / row.scrollWidth
  progress.value = Math.min(1, Math.max(0, row.scrollLeft / max))
}

function cardStep(row: HTMLElement) {
  const card = row.firstElementChild as HTMLElement | null
  const step = (card?.offsetWidth ?? 0) + (Number.parseFloat(getComputedStyle(row).columnGap) || 0)
  return step > 0 ? step : row.clientWidth / 2
}

function startDrag(event: PointerEvent) {
  const row = scrollerRef.value
  if (event.button !== 0 || !row || !trackRef.value || !thumbRef.value) return
  const track = trackRef.value.getBoundingClientRect()
  const thumb = thumbRef.value.getBoundingClientRect()
  const travel = track.width - thumb.width
  const max = row.scrollWidth - row.clientWidth
  if (travel <= 0 || max <= 0) return
  const scale = max / travel
  // Pressing the track beside the thumb first brings the thumb's centre under the pointer, like a range input.
  if (event.clientX < thumb.left || event.clientX > thumb.right) {
    const thumbLeft = Math.min(travel, Math.max(0, event.clientX - track.left - thumb.width / 2))
    row.scrollLeft = thumbLeft * scale
  }
  drag = { pointerId: event.pointerId, startX: event.clientX, startScroll: row.scrollLeft, scale }
  dragging.value = true
  const rail = event.currentTarget as HTMLElement
  rail.setPointerCapture?.(event.pointerId)
  sync()
}

function moveDrag(event: PointerEvent) {
  const row = scrollerRef.value
  if (!drag || event.pointerId !== drag.pointerId || !row) return
  row.scrollLeft = drag.startScroll + (event.clientX - drag.startX) * drag.scale
  sync()
}

function endDrag(event: PointerEvent) {
  if (!drag || event.pointerId !== drag.pointerId) return
  drag = null
  dragging.value = false
}

function onKeydown(event: KeyboardEvent) {
  const target = KEY_TARGETS[event.key]
  const row = scrollerRef.value
  if (!target || !row) return
  event.preventDefault()
  const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  row.scrollTo({ left: target(row), behavior: reduceMotion ? 'auto' : 'smooth' })
}

onMounted(() => {
  const row = scrollerRef.value
  if (!row) return
  sync()
  if (typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(sync)
    resizeObserver.observe(row)
  }
  // Adding or removing cards changes the scroll width without resizing the row itself.
  mutationObserver = new MutationObserver(sync)
  mutationObserver.observe(row, { childList: true })
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  mutationObserver?.disconnect()
})
</script>
