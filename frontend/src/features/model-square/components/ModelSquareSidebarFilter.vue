<template>
  <aside
    class="w-full flex-col rounded-2xl border border-gray-200/80 bg-white/90 p-4 shadow-sm backdrop-blur-md dark:border-dark-700/80 dark:bg-dark-900/80"
  >
    <!-- 头部标题与重置按钮 -->
    <div class="flex items-center justify-between pb-3.5 border-b border-gray-100 dark:border-dark-800">
      <div class="flex items-center gap-2">
        <Icon name="filter" size="sm" class="text-indigo-600 dark:text-indigo-400" />
        <span class="text-sm font-bold text-gray-900 dark:text-white">筛选</span>
        <span
          v-if="activeFilterCount > 0"
          class="flex h-5 min-w-[20px] items-center justify-center rounded-full bg-indigo-500/10 px-1.5 text-xs font-bold text-indigo-600 dark:text-indigo-400"
        >
          {{ activeFilterCount }}
        </span>
      </div>
      <button
        v-if="activeFilterCount > 0"
        type="button"
        class="text-xs font-semibold text-gray-500 hover:text-indigo-600 dark:text-dark-400 dark:hover:text-indigo-400 transition-colors"
        @click="$emit('reset')"
      >
        重置全部
      </button>
    </div>

    <div class="mt-4 space-y-6">
      <!-- 提供商筛选 -->
      <div>
        <div class="flex items-center justify-between mb-2.5">
          <span class="text-xs font-bold uppercase tracking-wider text-gray-500 dark:text-dark-400">提供商</span>
          <div class="flex items-center gap-1.5 text-[11px]">
            <button
              type="button"
              class="text-indigo-600 dark:text-indigo-400 hover:underline font-medium"
              @click="selectAllPlatforms"
            >
              全选
            </button>
            <span class="text-gray-300 dark:text-dark-600">/</span>
            <button
              type="button"
              class="text-gray-500 dark:text-dark-400 hover:underline"
              @click="clearPlatforms"
            >
              清空
            </button>
          </div>
        </div>

        <!-- 平台搜索 -->
        <div class="relative mb-2">
          <Icon name="search" size="xs" class="absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            v-model="platformSearch"
            type="text"
            placeholder="搜索平台..."
            class="w-full rounded-lg border border-gray-200 bg-gray-50/50 py-1.5 pl-8 pr-2.5 text-xs text-gray-800 placeholder-gray-400 outline-none focus:border-indigo-500 focus:bg-white dark:border-dark-700 dark:bg-dark-800/60 dark:text-gray-200 dark:placeholder-dark-500 dark:focus:border-indigo-500"
          />
        </div>

        <!-- 平台复选列表 -->
        <div class="max-h-48 space-y-1 overflow-y-auto pr-1">
          <label
            v-for="p in filteredAvailablePlatforms"
            :key="p"
            class="flex items-center justify-between rounded-lg px-2 py-1.5 text-xs text-gray-700 transition-colors hover:bg-gray-100/70 dark:text-gray-300 dark:hover:bg-dark-800/70 cursor-pointer select-none"
          >
            <div class="flex items-center gap-2 min-w-0">
              <input
                type="checkbox"
                :checked="isPlatformChecked(p)"
                class="h-3.5 w-3.5 rounded border-gray-300 text-indigo-600 focus:ring-indigo-500 dark:border-dark-600 dark:bg-dark-800"
                @change="$emit('toggle-platform', p)"
              />
              <PlatformIcon :platform="p" size="xs" />
              <span class="truncate font-medium">{{ platformLabel(p) }}</span>
            </div>
            <span class="text-[11px] font-mono text-gray-400 dark:text-dark-500 ml-2">
              {{ platformCounts[p] || 0 }}
            </span>
          </label>
        </div>
      </div>

      <!-- 能力特性筛选 -->
      <div>
        <div class="flex items-center justify-between mb-2">
          <span class="text-xs font-bold uppercase tracking-wider text-gray-500 dark:text-dark-400">能力特性</span>
          <button
            v-if="selectedCapabilities.length > 0"
            type="button"
            class="text-[11px] text-gray-400 hover:text-indigo-500"
            @click="clearCapabilities"
          >
            清空
          </button>
        </div>
        <div class="grid grid-cols-2 gap-1.5">
          <button
            v-for="cap in allCapabilities"
            :key="cap"
            type="button"
            class="flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-left text-xs font-medium transition-all"
            :class="
              selectedCapabilities.includes(cap)
                ? 'border-indigo-500/50 bg-indigo-50/70 text-indigo-700 dark:border-indigo-500/40 dark:bg-indigo-950/40 dark:text-indigo-300'
                : 'border-gray-200 bg-white/50 text-gray-600 hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-800/40 dark:text-dark-300 dark:hover:bg-dark-800'
            "
            @click="$emit('toggle-capability', cap)"
          >
            <Icon :name="capabilityMeta(cap).icon as any" size="xs" />
            <span class="truncate">{{ capabilityMeta(cap).label }}</span>
          </button>
        </div>
      </div>

      <!-- 价格区间 -->
      <div>
        <span class="text-xs font-bold uppercase tracking-wider text-gray-500 dark:text-dark-400 block mb-2">价格区间 (每 1M tokens)</span>
        <div class="flex flex-wrap gap-1.5">
          <button
            v-for="item in priceRangeOptions"
            :key="item.value"
            type="button"
            class="rounded-lg border px-2.5 py-1 text-xs font-medium transition-all"
            :class="
              priceRange === item.value
                ? 'border-indigo-500/60 bg-indigo-50 text-indigo-700 dark:border-indigo-500/50 dark:bg-indigo-950/40 dark:text-indigo-300 shadow-sm'
                : 'border-gray-200 bg-white/50 text-gray-600 hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-800/40 dark:text-dark-300 dark:hover:bg-dark-800'
            "
            @click="$emit('update:priceRange', item.value)"
          >
            {{ item.label }}
          </button>
        </div>
      </div>

      <!-- 上下文长度 -->
      <div>
        <span class="text-xs font-bold uppercase tracking-wider text-gray-500 dark:text-dark-400 block mb-2">上下文长度</span>
        <div class="flex flex-wrap gap-1.5">
          <button
            v-for="item in contextRangeOptions"
            :key="item.value"
            type="button"
            class="rounded-lg border px-2.5 py-1 text-xs font-medium transition-all"
            :class="
              contextRange === item.value
                ? 'border-indigo-500/60 bg-indigo-50 text-indigo-700 dark:border-indigo-500/50 dark:bg-indigo-950/40 dark:text-indigo-300 shadow-sm'
                : 'border-gray-200 bg-white/50 text-gray-600 hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-800/40 dark:text-dark-300 dark:hover:bg-dark-800'
            "
            @click="$emit('update:contextRange', item.value)"
          >
            {{ item.label }}
          </button>
        </div>
      </div>

      <!-- 计费模式 -->
      <div>
        <span class="text-xs font-bold uppercase tracking-wider text-gray-500 dark:text-dark-400 block mb-2">计费类型</span>
        <div class="flex flex-wrap gap-1.5">
          <button
            v-for="item in billingTypeOptions"
            :key="item.value"
            type="button"
            class="rounded-lg border px-2.5 py-1 text-xs font-medium transition-all"
            :class="
              billingType === item.value
                ? 'border-indigo-500/60 bg-indigo-50 text-indigo-700 dark:border-indigo-500/50 dark:bg-indigo-950/40 dark:text-indigo-300 shadow-sm'
                : 'border-gray-200 bg-white/50 text-gray-600 hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-800/40 dark:text-dark-300 dark:hover:bg-dark-800'
            "
            @click="$emit('update:billingType', item.value)"
          >
            {{ item.label }}
          </button>
        </div>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { platformLabel } from '@/utils/platformColors'
import { capabilityMeta } from '../utils/capabilities'
import type { BillingTypeFilter, ContextRange, ModelCapability, PriceRange } from '../types'

interface Props {
  platforms: string[]
  platformCounts: Record<string, number>
  selectedPlatforms: string[]
  selectedCapabilities: ModelCapability[]
  priceRange: PriceRange
  contextRange: ContextRange
  billingType: BillingTypeFilter
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'toggle-platform': [platform: string]
  'set-platforms': [platforms: string[]]
  'toggle-capability': [cap: ModelCapability]
  'set-capabilities': [caps: ModelCapability[]]
  'update:priceRange': [range: PriceRange]
  'update:contextRange': [range: ContextRange]
  'update:billingType': [type: BillingTypeFilter]
  reset: []
}>()

const platformSearch = ref('')

const allCapabilities: ModelCapability[] = ['vision', 'tool_call', 'reasoning', 'audio', 'image_gen', 'embedding']

const priceRangeOptions: { label: string; value: PriceRange }[] = [
  { label: '全部', value: 'all' },
  { label: '免费', value: 'free' },
  { label: '< $1', value: 'lt1' },
  { label: '$1 - $5', value: '1to5' },
  { label: '$5 - $15', value: '5to15' },
  { label: '> $15', value: 'gt15' },
]

const contextRangeOptions: { label: string; value: ContextRange }[] = [
  { label: '全部', value: 'all' },
  { label: '< 32K', value: 'lt32k' },
  { label: '32K - 128K', value: '32kTo128k' },
  { label: '128K - 256K', value: '128kTo256k' },
  { label: '> 256K', value: 'gt256k' },
]

const billingTypeOptions: { label: string; value: BillingTypeFilter }[] = [
  { label: '全部', value: 'all' },
  { label: 'Token 计费', value: 'tokens' },
  { label: '按次/张计费', value: 'request' },
  { label: '阶梯价格', value: 'intervals' },
]

const availablePlatforms = computed(() => props.platforms.filter((p) => p !== 'all'))

const filteredAvailablePlatforms = computed(() => {
  const q = platformSearch.value.trim().toLowerCase()
  if (!q) return availablePlatforms.value
  return availablePlatforms.value.filter((p) => p.toLowerCase().includes(q) || platformLabel(p).toLowerCase().includes(q))
})

const activeFilterCount = computed(() => {
  let count = 0
  if (props.selectedPlatforms.length > 0) count += props.selectedPlatforms.length
  if (props.selectedCapabilities.length > 0) count += props.selectedCapabilities.length
  if (props.priceRange !== 'all') count += 1
  if (props.contextRange !== 'all') count += 1
  if (props.billingType !== 'all') count += 1
  return count
})

function isPlatformChecked(p: string) {
  return props.selectedPlatforms.includes(p)
}

function selectAllPlatforms() {
  emit('set-platforms', [...availablePlatforms.value])
}

function clearPlatforms() {
  emit('set-platforms', [])
}

function clearCapabilities() {
  emit('set-capabilities', [])
}
</script>
