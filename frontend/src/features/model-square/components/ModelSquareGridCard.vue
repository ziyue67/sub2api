<template>
  <div
    class="group relative flex flex-col justify-between rounded-2xl border border-gray-200/80 bg-white/90 p-5 shadow-sm transition-all duration-200 hover:-translate-y-0.5 hover:border-indigo-500/30 hover:shadow-md dark:border-dark-700/80 dark:bg-dark-900/90 dark:hover:border-indigo-500/30"
  >
    <!-- 头部：图标 + 模型名称 + 平台标签 + 渠道数 -->
    <div>
      <div class="flex items-start justify-between gap-3">
        <div class="flex items-center gap-3 min-w-0">
          <div
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-gray-100/90 text-gray-800 shadow-inner dark:bg-dark-800/90 dark:text-gray-100"
            :class="platformTextClass(model.platform)"
          >
            <PlatformIcon :platform="model.platform" size="md" />
          </div>
          <div class="min-w-0">
            <h3
              class="truncate text-base font-bold text-gray-900 group-hover:text-indigo-600 transition-colors dark:text-white dark:group-hover:text-indigo-400"
              :title="model.name"
            >
              {{ model.name }}
            </h3>
            <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-400 mt-0.5">
              <span class="font-medium">{{ platformLabel(model.platform) }}</span>
              <span>·</span>
              <span class="font-mono">{{ model.channels.length }} 渠道</span>
              <span v-if="model.totalAccounts" class="font-mono">({{ model.totalAccounts }} 账号)</span>
            </div>
          </div>
        </div>

        <!-- 专属折扣或倍率徽章 -->
        <span
          v-if="model.bestMultiplier != null && model.bestMultiplier < 1"
          class="shrink-0 rounded-md bg-rose-50 px-2 py-0.5 text-[11px] font-bold text-rose-600 dark:bg-rose-950/40 dark:text-rose-400 border border-rose-500/20"
        >
          {{ formatMultiplier(model.bestMultiplier) }}
        </span>
      </div>

      <!-- 核心指标网格：上下文、输入价格、输出价格 -->
      <div class="mt-4 grid grid-cols-3 gap-2 rounded-xl bg-gray-50/80 p-3 text-center dark:bg-dark-950/50 border border-gray-100 dark:border-dark-800/80">
        <!-- 上下文 -->
        <div class="flex flex-col">
          <span class="text-[10px] font-bold uppercase tracking-wider text-gray-400 dark:text-dark-500">上下文</span>
          <span class="text-xs font-black text-gray-800 dark:text-gray-200 mt-0.5 font-mono">
            {{ model.contextWindow || '128K' }}
          </span>
        </div>
        <!-- 输入价 -->
        <div class="flex flex-col">
          <span class="text-[10px] font-bold uppercase tracking-wider text-gray-400 dark:text-dark-500">输入价格</span>
          <span class="text-xs font-black text-emerald-600 dark:text-emerald-400 mt-0.5 font-mono">
            {{ formatPriceDisplay(model.minInputPrice) }}
          </span>
        </div>
        <!-- 输出价 -->
        <div class="flex flex-col">
          <span class="text-[10px] font-bold uppercase tracking-wider text-gray-400 dark:text-dark-500">输出价格</span>
          <span class="text-xs font-black text-blue-600 dark:text-blue-400 mt-0.5 font-mono">
            {{ formatPriceDisplay(model.minOutputPrice) }}
          </span>
        </div>
      </div>

      <!-- 模型特性胶囊 -->
      <div v-if="model.capabilities && model.capabilities.length > 0" class="mt-3 flex flex-wrap gap-1.5">
        <span
          v-for="cap in model.capabilities"
          :key="cap"
          class="inline-flex items-center gap-1 rounded-md bg-gray-100/80 px-2 py-0.5 text-[11px] font-medium text-gray-600 dark:bg-dark-800/80 dark:text-dark-300"
          :title="capabilityMeta(cap).description"
        >
          <Icon :name="capabilityMeta(cap).icon as any" size="xs" />
          <span>{{ capabilityMeta(cap).label }}</span>
        </span>
      </div>
    </div>

    <!-- 底部操作与渠道展开 -->
    <div class="mt-4 pt-3 border-t border-gray-100 dark:border-dark-800/80 flex items-center justify-between gap-2">
      <!-- 快捷复制模型名称 -->
      <button
        type="button"
        class="inline-flex items-center gap-1 text-xs font-medium text-gray-500 hover:text-indigo-600 dark:text-dark-400 dark:hover:text-indigo-400 transition-colors"
        @click="copyModelName"
      >
        <Icon :name="copied ? 'check' : 'copy'" size="xs" :class="copied ? 'text-emerald-500' : ''" />
        <span>{{ copied ? '已复制' : '复制模型' }}</span>
      </button>

      <!-- 查看详情/全渠道弹窗 -->
      <button
        type="button"
        class="inline-flex items-center gap-1 text-xs font-bold text-indigo-600 hover:text-indigo-700 dark:text-indigo-400 dark:hover:text-indigo-300"
        @click="$emit('view-details', model)"
      >
        <span>渠道与阶梯定价</span>
        <Icon name="arrowRight" size="xs" />
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { platformLabel, platformTextClass } from '@/utils/platformColors'
import { capabilityMeta } from '../utils/capabilities'
import { formatMultiplier, formatTokenPrice } from '../utils/pricing'
import type { ModelSquareModel } from '../types'

interface Props {
  model: ModelSquareModel
  userGroupRates: Record<number, number>
}

const props = defineProps<Props>()

defineEmits<{
  'view-details': [model: ModelSquareModel]
}>()

const copied = ref(false)

function formatPriceDisplay(val?: number | null) {
  if (val == null) return '免费 / 未设'
  return formatTokenPrice(val)
}

function copyModelName() {
  navigator.clipboard.writeText(props.model.name)
  copied.value = true
  setTimeout(() => {
    copied.value = false
  }, 2000)
}
</script>
