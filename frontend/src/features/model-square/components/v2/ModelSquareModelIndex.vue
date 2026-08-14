<template>
  <aside class='hidden lg:flex sticky top-6 h-fit max-h-[calc(100vh-3rem)] w-full flex-col overflow-hidden rounded-2xl border border-gray-200 dark:border-dark-800 bg-white/80 dark:bg-dark-900/80 backdrop-blur shadow-sm'>
    <div class='px-4 py-3 border-b border-gray-100 dark:border-dark-800'>
      <h2 class='text-xs font-black uppercase tracking-widest text-gray-500 dark:text-dark-400'>模型索引</h2>
      <p class='mt-0.5 text-[10px] text-gray-400 dark:text-dark-500'>点击跳转，共 {{ models.length }} 个</p>
    </div>
    <div class='overflow-y-auto p-2 space-y-1'>
      <button
        v-for='model in models'
        :key='model.key'
        type='button'
        class='w-full text-left rounded-xl px-3 py-2 text-xs font-bold transition-all'
        :class='activeKey === model.key ? activeClass : inactiveClass'
        @click='selectModel(model.key)'
      >
        <span class='block truncate'>{{ model.name }}</span>
        <span class='mt-0.5 block text-[10px] opacity-70 font-mono uppercase tracking-wider'>{{ model.platform }}</span>
      </button>
      <p v-if='models.length === 0' class='px-3 py-6 text-center text-xs text-gray-400 dark:text-dark-500'>没有匹配的模型</p>
    </div>
  </aside>
</template>

<script setup lang='ts'>
import { onMounted, onUnmounted } from 'vue'
import type { ModelSquareModel } from '../../types'

interface Props {
  models: ModelSquareModel[]
}

defineProps<Props>()

const activeKey = defineModel<string | null>('activeKey', { default: null })

const activeClass = 'bg-primary-500 text-white shadow-sm'
const inactiveClass = 'text-gray-600 hover:bg-gray-50 dark:text-dark-300 dark:hover:bg-dark-800'

function selectModel(key: string) {
  activeKey.value = key
  const el = document.getElementById('model-' + key)
  if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

let observer: IntersectionObserver | null = null

onMounted(() => {
  observer = new IntersectionObserver((entries) => {
    const visible = entries.filter((e) => e.isIntersecting)
    if (visible.length > 0) {
      const top = visible.sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)[0]
      const key = top.target.getAttribute('data-model-key')
      if (key) activeKey.value = key
    }
  }, { rootMargin: '-10% 0px -60% 0px', threshold: 0 })
  document.querySelectorAll('[data-model-key]').forEach((el) => observer?.observe(el))
})

onUnmounted(() => {
  if (observer) observer.disconnect()
})
</script>
