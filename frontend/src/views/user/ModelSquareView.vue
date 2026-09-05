<template>
  <AppLayout>
    <ModelSquareBackground :is-dark='isDark' />

    <div
      class='relative z-10 min-h-screen transition-colors duration-500'
      :class='isDark ? "dark bg-[#0a0c10]" : "bg-slate-50"'
    >
      <div class='mx-auto w-full max-w-[1600px] px-4 py-10 sm:px-6 lg:px-8'>
        <!-- 顶栏介绍与统计 -->
        <ModelSquareHeader
          :search='search'
          :loading='loading'
          :is-dark='isDark'
          :total-models='modelGroups.length'
          :total-channels='totalChannelsCount'
          @update:search='setSearch'
          @refresh='loadModels'
          @toggle-dark='isDark = !isDark'
        />

        <!-- 移动端筛选抽屉按钮 -->
        <div class="mb-4 lg:hidden flex justify-end">
          <button
            type="button"
            class="inline-flex items-center gap-2 rounded-xl border border-gray-200 bg-white/90 px-4 py-2 text-xs font-bold text-gray-700 shadow-sm dark:border-dark-700 dark:bg-dark-900 dark:text-gray-200"
            @click="mobileFilterOpen = !mobileFilterOpen"
          >
            <Icon name="filter" size="xs" class="text-indigo-500" />
            <span>{{ mobileFilterOpen ? '收起筛选' : '展开条件筛选' }}</span>
          </button>
        </div>

        <!-- 主区域布局：左侧筛选 + 右侧工具栏与列表/卡片 -->
        <div class="grid grid-cols-1 lg:grid-cols-[280px_1fr] xl:grid-cols-[300px_1fr] gap-6 items-start">
          <!-- 左侧边栏筛选：桌面端粘顶，移动端可折叠 -->
          <div :class="mobileFilterOpen ? 'block' : 'hidden lg:block'" class="lg:sticky lg:top-6 z-20">
            <ModelSquareSidebarFilter
              :platforms="platforms"
              :platform-counts="platformCounts"
              :selected-platforms="selectedPlatforms"
              :selected-capabilities="selectedCapabilities"
              :price-range="priceRange"
              :context-range="contextRange"
              :billing-type="billingType"
              @toggle-platform="togglePlatform"
              @set-platforms="selectedPlatforms = $event"
              @toggle-capability="toggleCapability"
              @set-capabilities="selectedCapabilities = $event"
              @update:price-range="priceRange = $event"
              @update:context-range="contextRange = $event"
              @update:billing-type="billingType = $event"
              @reset="resetFilters"
            />
          </div>

          <!-- 右侧内容区 -->
          <div class="min-w-0 space-y-4">
            <!-- 顶部搜索工具栏与视图模式切换 -->
            <ModelSquareToolbar
              :search="search"
              :loading="loading"
              :total-count="modelGroups.length"
              :filtered-count="filteredModels.length"
              :sort-option="sortOption"
              :view-mode="viewMode"
              :selected-platforms="selectedPlatforms"
              :selected-capabilities="selectedCapabilities"
              :price-range="priceRange"
              :context-range="contextRange"
              :billing-type="billingType"
              @update:search="setSearch"
              @update:sort-option="sortOption = $event"
              @update:view-mode="viewMode = $event"
              @remove-platform="togglePlatform"
              @remove-capability="toggleCapability"
              @reset-price-range="priceRange = 'all'"
              @reset-all="resetFilters"
              @refresh="loadModels"
            />

            <ModelSquareHint />

            <!-- 加载与空状态 -->
            <ModelSquareLoading v-if="loading" />
            <ModelSquareEmpty v-else-if="filteredModels.length === 0" />

            <!-- 卡片网格视图 (OpenModel Style) -->
            <div
              v-else-if="viewMode === 'grid'"
              class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4"
            >
              <ModelSquareGridCard
                v-for="model in filteredModels"
                :key="model.key"
                :model="model"
                :user-group-rates="userGroupRates"
                @view-details="selectedModel = $event"
              />
            </div>

            <!-- 紧凑表格对比视图 (OpenModel Style) -->
            <ModelSquareTable
              v-else-if="viewMode === 'table'"
              :models="filteredModels"
              :user-group-rates="userGroupRates"
              @view-details="selectedModel = $event"
            />
          </div>
        </div>
      </div>

      <!-- 模型定价与代码详情弹窗 -->
      <ModelSquareDetailModal
        :model="selectedModel"
        :user-group-rates="userGroupRates"
        @close="selectedModel = null"
      />

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
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
import ModelSquareSidebarFilter from '@/features/model-square/components/ModelSquareSidebarFilter.vue'
import ModelSquareToolbar from '@/features/model-square/components/ModelSquareToolbar.vue'
import ModelSquareGridCard from '@/features/model-square/components/ModelSquareGridCard.vue'
import ModelSquareTable from '@/features/model-square/components/ModelSquareTable.vue'
import ModelSquareDetailModal from '@/features/model-square/components/ModelSquareDetailModal.vue'
import type { ModelSquareModel } from '@/features/model-square/types'

const { loading, userGroupRates, platforms, modelGroups, loadModels } = useModelSquare()
const { search, debouncedSearch, setSearch } = useModelSquareSearch()
const {
  selectedPlatforms,
  selectedCapabilities,
  priceRange,
  contextRange,
  billingType,
  sortOption,
  viewMode,
  filteredModels,
  togglePlatform,
  toggleCapability,
  resetFilters,
} = useModelSquareFilters({
  modelGroups,
  search: debouncedSearch,
})

const selectedModel = ref<ModelSquareModel | null>(null)
const mobileFilterOpen = ref(false)

const platformCounts = computed(() => {
  const counts: Record<string, number> = {}
  for (const model of modelGroups.value) {
    counts[model.platform] = (counts[model.platform] || 0) + 1
  }
  return counts
})

const totalChannelsCount = computed(() => {
  const channels = new Set<string>()
  for (const m of modelGroups.value) {
    for (const c of m.channels) {
      channels.add(c.key)
    }
  }
  return channels.size
})

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
