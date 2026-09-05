<template>
  <div class="flex flex-col gap-4">
    <!-- 上层主工具条 -->
    <div class="flex flex-col md:flex-row md:items-center justify-between gap-3">
      <!-- 搜索框 -->
      <div class="relative flex-1 max-w-lg">
        <Icon
          name="search"
          size="sm"
          class="absolute left-3.5 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-500"
        />
        <input
          :value="search"
          type="text"
          class="w-full h-10 rounded-xl border border-gray-200/80 bg-white/90 pl-10 pr-9 text-sm text-gray-900 placeholder-gray-400 shadow-sm backdrop-blur outline-none transition-all focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20 dark:border-dark-700/80 dark:bg-dark-900/90 dark:text-gray-100 dark:placeholder-dark-500 dark:focus:border-indigo-400"
          placeholder="搜索模型名称、提供商、渠道、分组..."
          @input="$emit('update:search', ($event.target as HTMLInputElement).value)"
        />
        <button
          v-if="search"
          type="button"
          class="absolute right-3 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200"
          @click="$emit('update:search', '')"
        >
          <Icon name="x" size="xs" />
        </button>
      </div>

      <!-- 右侧操作栏：排序、视图模式切换、刷新 -->
      <div class="flex items-center gap-2.5 shrink-0">
        <!-- 排序下拉菜单 -->
        <div class="relative">
          <select
            :value="sortOption"
            class="h-10 appearance-none rounded-xl border border-gray-200/80 bg-white/90 pl-3 pr-8 text-xs font-semibold text-gray-700 shadow-sm outline-none transition-all hover:bg-gray-50 focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20 dark:border-dark-700/80 dark:bg-dark-900/90 dark:text-gray-200 dark:hover:bg-dark-800"
            @change="$emit('update:sortOption', ($event.target as HTMLSelectElement).value as SortOption)"
          >
            <option value="default">默认排序</option>
            <option value="price_asc">输入价格: 低到高</option>
            <option value="price_desc">输入价格: 高到低</option>
            <option value="output_price_asc">输出价格: 低到高</option>
            <option value="output_price_desc">输出价格: 高到低</option>
            <option value="channels_desc">渠道数量最多</option>
            <option value="name_asc">模型名称 A-Z</option>
          </select>
          <Icon
            name="chevronDown"
            size="xs"
            class="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-gray-400"
          />
        </div>

        <!-- 视图切换（卡片网格 vs 紧凑表格） -->
        <div class="flex items-center rounded-xl border border-gray-200/80 bg-white/90 p-1 shadow-sm backdrop-blur dark:border-dark-700/80 dark:bg-dark-900/90">
          <button
            type="button"
            class="flex h-8 w-8 items-center justify-center rounded-lg transition-colors"
            :class="
              viewMode === 'grid'
                ? 'bg-indigo-500/10 text-indigo-600 dark:bg-indigo-500/20 dark:text-indigo-400 font-bold'
                : 'text-gray-400 hover:text-gray-600 dark:text-dark-400 dark:hover:text-gray-200'
            "
            title="网格视图"
            @click="$emit('update:viewMode', 'grid')"
          >
            <Icon name="grid" size="xs" />
          </button>
          <button
            type="button"
            class="flex h-8 w-8 items-center justify-center rounded-lg transition-colors"
            :class="
              viewMode === 'table'
                ? 'bg-indigo-500/10 text-indigo-600 dark:bg-indigo-500/20 dark:text-indigo-400 font-bold'
                : 'text-gray-400 hover:text-gray-600 dark:text-dark-400 dark:hover:text-gray-200'
            "
            title="表格视图"
            @click="$emit('update:viewMode', 'table')"
          >
            <Icon name="table" size="xs" />
          </button>
        </div>

        <!-- 刷新按钮 -->
        <button
          type="button"
          class="flex h-10 w-10 items-center justify-center rounded-xl border border-gray-200/80 bg-white/90 shadow-sm backdrop-blur hover:bg-gray-50 active:scale-95 transition-all dark:border-dark-700/80 dark:bg-dark-900/90 dark:hover:bg-dark-800"
          :disabled="loading"
          title="刷新数据"
          @click="$emit('refresh')"
        >
          <Icon name="refresh" size="xs" :class="loading ? 'animate-spin text-indigo-500' : 'text-gray-500 dark:text-dark-400'" />
        </button>
      </div>
    </div>

    <!-- 底部状态指示与激活筛选标签 -->
    <div class="flex flex-wrap items-center justify-between gap-2 pt-1 text-xs text-gray-500 dark:text-dark-400">
      <div class="flex items-center gap-2">
        <span>共 <strong class="font-bold text-gray-800 dark:text-gray-200">{{ totalCount }}</strong> 个模型</span>
        <span>·</span>
        <span>当前展示 <strong class="font-bold text-indigo-600 dark:text-indigo-400">{{ filteredCount }}</strong> 个</span>
      </div>

      <!-- 激活中的过滤标签 chips -->
      <div v-if="hasActiveFilters" class="flex flex-wrap items-center gap-1.5">
        <span
          v-for="p in selectedPlatforms"
          :key="'p-' + p"
          class="inline-flex items-center gap-1 rounded-md bg-gray-100 px-2 py-0.5 text-[11px] font-medium text-gray-700 dark:bg-dark-800 dark:text-gray-300"
        >
          {{ platformLabel(p) }}
          <button type="button" @click="$emit('remove-platform', p)"><Icon name="x" size="xs" /></button>
        </span>
        <span
          v-for="cap in selectedCapabilities"
          :key="'c-' + cap"
          class="inline-flex items-center gap-1 rounded-md bg-indigo-50 px-2 py-0.5 text-[11px] font-medium text-indigo-700 dark:bg-indigo-950/40 dark:text-indigo-300"
        >
          {{ capabilityMeta(cap).label }}
          <button type="button" @click="$emit('remove-capability', cap)"><Icon name="x" size="xs" /></button>
        </span>
        <span
          v-if="priceRange !== 'all'"
          class="inline-flex items-center gap-1 rounded-md bg-amber-50 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:bg-amber-950/40 dark:text-amber-300"
        >
          价格: {{ priceRange }}
          <button type="button" @click="$emit('reset-price-range')"><Icon name="x" size="xs" /></button>
        </span>
        <button
          type="button"
          class="text-[11px] font-medium text-indigo-600 hover:underline dark:text-indigo-400 ml-1"
          @click="$emit('reset-all')"
        >
          清除全部
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { platformLabel } from '@/utils/platformColors'
import { capabilityMeta } from '../utils/capabilities'
import type { BillingTypeFilter, ContextRange, ModelCapability, PriceRange, SortOption, ViewMode } from '../types'

interface Props {
  search: string
  loading: boolean
  totalCount: number
  filteredCount: number
  sortOption: SortOption
  viewMode: ViewMode
  selectedPlatforms: string[]
  selectedCapabilities: ModelCapability[]
  priceRange: PriceRange
  contextRange: ContextRange
  billingType: BillingTypeFilter
}

const props = defineProps<Props>()

defineEmits<{
  'update:search': [value: string]
  'update:sortOption': [value: SortOption]
  'update:viewMode': [mode: ViewMode]
  'remove-platform': [p: string]
  'remove-capability': [cap: ModelCapability]
  'reset-price-range': []
  'reset-all': []
  refresh: []
}>()

const hasActiveFilters = computed(() => {
  return (
    props.selectedPlatforms.length > 0 ||
    props.selectedCapabilities.length > 0 ||
    props.priceRange !== 'all' ||
    props.contextRange !== 'all' ||
    props.billingType !== 'all'
  )
})
</script>
