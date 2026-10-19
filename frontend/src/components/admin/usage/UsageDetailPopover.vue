<template>
  <BaseDialog v-if="isMobile" :show="show" :title="title" width="narrow" close-on-click-outside @close="$emit('close')">
    <slot />
  </BaseDialog>
  <Teleport v-else to="body">
    <div
      v-if="show"
      ref="panel"
      role="tooltip"
      class="fixed z-[9999] max-w-[calc(100vw-1rem)] max-h-[calc(100dvh-1rem)] overflow-auto"
      :style="{ left: position.x + 'px', top: position.y + 'px' }"
      @mouseenter="$emit('mouseenter')"
      @mouseleave="$emit('mouseleave')"
    ><slot /></div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watchPostEffect } from 'vue'
import { useResizeObserver, useWindowSize } from '@vueuse/core'
import BaseDialog from '@/components/common/BaseDialog.vue'

const props = defineProps<{ show: boolean; title: string; anchor: DOMRect | null }>()
defineEmits<{ close: []; mouseenter: []; mouseleave: [] }>()
const { width, height } = useWindowSize()
const isMobile = computed(() => width.value < 768)
const panel = ref<HTMLElement | null>(null)
const position = ref({ x: 8, y: 8 })
function reposition() {
  if (!panel.value || !props.anchor) return
  // Measure the rendered content, including localized labels, not guessed widths.
  const bounds = panel.value.getBoundingClientRect()
  const anchor = props.anchor
  const preferred = anchor.right + 8 + bounds.width <= width.value - 8
    ? anchor.right + 8 : anchor.left - bounds.width - 8
  const next = {
    x: Math.max(8, Math.min(preferred, width.value - bounds.width - 8)),
    y: Math.max(8, Math.min(anchor.top + anchor.height / 2 - bounds.height / 2, height.value - bounds.height - 8)),
  }
  if (position.value.x !== next.x || position.value.y !== next.y) position.value = next
}
watchPostEffect(reposition)
useResizeObserver(panel, reposition)
</script>
