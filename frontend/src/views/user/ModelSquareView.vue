<template>
  <AppLayout>
    <ModelSquareBackground :is-dark='isDark' />

    <div
      class='relative z-10 min-h-screen transition-colors duration-500'
      :class='isDark ? "dark bg-[#0a0c10]" : "bg-slate-50"'
    >
      <div class='mx-auto w-full max-w-[1600px] px-4 py-10 sm:px-6 lg:px-8'>
        <ModelSquareHeader
          :search='search'
          :loading='loading'
          :is-dark='isDark'
          @update:search='setSearch'
          @refresh='loadModels'
          @toggle-dark='isDark = !isDark'
        />

        <ModelSquarePlatformFilter
          :model-value='platform'
          :platforms='platforms'
          @update:model-value='setPlatform'
        />

        <ModelSquareHint />

        <ModelSquareLoading v-if='loading' />
        <ModelSquareEmpty v-else-if='filteredModels.length === 0' />

        <div v-else class='model-square-scroll-region' tabindex='0'>
          <div class='model-square-scroll-content grid grid-cols-1 lg:grid-cols-[320px_1fr] gap-8 items-start'>
            <ModelSquareModelIndex
              v-model='activeModelKey'
              :models='filteredModels'
              :search='debouncedSearch'
            />
            <div class='space-y-8'>
              <ModelSquareModelCard
                v-for='model in filteredModels'
                :key='model.key'
                :model='model'
                :user-group-rates='userGroupRates'
              />
            </div>
          </div>
        </div>
      </div>

      <button
        v-show='showBackToTop'
        type='button'
        class='fixed bottom-8 right-8 z-50 flex items-center gap-2 rounded-2xl bg-indigo-500/90 px-4 py-3 text-sm font-semibold text-white shadow-lg shadow-indigo-500/30 backdrop-blur transition-all hover:scale-105 hover:bg-indigo-500 active:scale-95'
        aria-label='返回顶部'
        @click='scrollToTop'
      >
        <Icon name='arrowUp' size='sm' />
        <span class='hidden sm:inline'>返回顶部</span>
      </button>
    </div>
  </AppLayout>
</template>

<script setup lang='ts'>
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useModelSquare } from '@/features/model-square/composables/useModelSquare'
import { useModelSquareFilters } from '@/features/model-square/composables/useModelSquareFilters'
import { useModelSquareSearch } from '@/features/model-square/composables/useModelSquareSearch'
import ModelSquareBackground from '@/features/model-square/components/ModelSquareBackground.vue'
import ModelSquareEmpty from '@/features/model-square/components/ModelSquareEmpty.vue'
import ModelSquareHeader from '@/features/model-square/components/v2/ModelSquareHeader.vue'
import ModelSquareHint from '@/features/model-square/components/ModelSquareHint.vue'
import ModelSquareLoading from '@/features/model-square/components/ModelSquareLoading.vue'
import ModelSquarePlatformFilter from '@/features/model-square/components/ModelSquarePlatformFilter.vue'
import ModelSquareModelCard from '@/features/model-square/components/v2/ModelSquareModelCard.vue'
import ModelSquareModelIndex from '@/features/model-square/components/v2/ModelSquareModelIndex.vue'

const { loading, userGroupRates, platforms, modelGroups, loadModels } = useModelSquare()
const { search, debouncedSearch, setSearch } = useModelSquareSearch()
const { platform, setPlatform, filteredModels } = useModelSquareFilters({
  modelGroups,
  search: debouncedSearch,
})

const activeModelKey = ref<string | null>(null)

// 局部暗色：只影响本页容器，不污染 html 根元素
const isDark = ref(true)

const showBackToTop = ref(false)

function onScroll() {
  showBackToTop.value = (window.scrollY || document.documentElement.scrollTop) > 300
}

function scrollToTop() {
  window.scrollTo({ top: 0, behavior: 'smooth' })
}

onMounted(() => {
  const saved = localStorage.getItem('model-square-theme')
  if (saved === 'light' || saved === 'dark') {
    isDark.value = saved === 'dark'
  }
  window.addEventListener('scroll', onScroll, { passive: true })
})

onBeforeUnmount(() => {
  window.removeEventListener('scroll', onScroll)
})

watch(isDark, (value) => {
  localStorage.setItem('model-square-theme', value ? 'dark' : 'light')
})
</script>

<style scoped>
.model-square-scroll-region {
  min-width: 0;
  overflow-x: auto;
  overscroll-behavior-x: contain;
  /* 给可见横条留出空间，避免紧贴卡片 */
  padding-bottom: 0.75rem;
  /*
   * 浏览器缩放（zoom）适配：
   * 缩放会等比缩小视口的 CSS 宽度，内容 min-width 超出时由此区域横向滚动，
   * 布局本身按 rem/断点等比降级，不会出现错位或裁剪。
   */
  /* Firefox：始终可见的横向滚动条（覆盖全局“仅 hover 可见”的透明样式） */
  scrollbar-width: thin;
  scrollbar-color: rgba(99, 102, 241, 0.45) rgba(148, 163, 184, 0.12);
}

.model-square-scroll-region:hover,
.model-square-scroll-region:focus-visible {
  scrollbar-color: rgba(99, 102, 241, 0.7) rgba(148, 163, 184, 0.16);
}

.dark .model-square-scroll-region {
  scrollbar-color: rgba(129, 140, 248, 0.5) rgba(255, 255, 255, 0.06);
}

.dark .model-square-scroll-region:hover,
.dark .model-square-scroll-region:focus-visible {
  scrollbar-color: rgba(129, 140, 248, 0.75) rgba(255, 255, 255, 0.1);
}

/* WebKit / Blink：始终可见的横向滚动条 */
.model-square-scroll-region::-webkit-scrollbar {
  height: 0.625rem;
  width: 0.625rem;
}

.model-square-scroll-region::-webkit-scrollbar-track {
  background: rgba(148, 163, 184, 0.12);
  border-radius: 9999px;
}

.model-square-scroll-region::-webkit-scrollbar-thumb {
  background: rgba(99, 102, 241, 0.45);
  border-radius: 9999px;
}

.model-square-scroll-region::-webkit-scrollbar-thumb:hover {
  background: rgba(99, 102, 241, 0.7);
}

.dark .model-square-scroll-region::-webkit-scrollbar-track {
  background: rgba(255, 255, 255, 0.06);
}

.dark .model-square-scroll-region::-webkit-scrollbar-thumb {
  background: rgba(129, 140, 248, 0.5);
}

.dark .model-square-scroll-region::-webkit-scrollbar-thumb:hover {
  background: rgba(129, 140, 248, 0.75);
}

.model-square-scroll-content {
  min-width: 75rem;
  padding: 0.125rem 0.25rem 1rem;
}

@media (max-width: 640px) {
  .model-square-scroll-content {
    min-width: 37.5rem;
  }
}
</style>
