<template>
  <div class="overflow-x-auto rounded-2xl border border-gray-200/80 bg-white/90 shadow-sm backdrop-blur dark:border-dark-700/80 dark:bg-dark-900/90">
    <table class="w-full text-left text-xs text-gray-600 dark:text-dark-300">
      <thead class="bg-gray-50/80 text-[11px] font-bold uppercase tracking-wider text-gray-400 dark:bg-dark-950/40 dark:text-dark-500 border-b border-gray-100 dark:border-dark-800">
        <tr>
          <th scope="col" class="py-3.5 pl-4 pr-3">模型名称</th>
          <th scope="col" class="px-3 py-3.5">提供商</th>
          <th scope="col" class="px-3 py-3.5">上下文</th>
          <th scope="col" class="px-3 py-3.5">能力特性</th>
          <th scope="col" class="px-3 py-3.5">输入价格 (1M)</th>
          <th scope="col" class="px-3 py-3.5">输出价格 (1M)</th>
          <th scope="col" class="px-3 py-3.5">可用渠道</th>
          <th scope="col" class="py-3.5 pl-3 pr-4 text-right">操作</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-gray-100 dark:divide-dark-800/80 font-medium">
        <tr
          v-for="model in models"
          :key="model.key"
          class="hover:bg-gray-50/70 dark:hover:bg-dark-800/50 transition-colors"
        >
          <!-- 模型名称 -->
          <td class="py-3 pl-4 pr-3">
            <div class="flex items-center gap-2.5">
              <div
                class="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-gray-100 dark:bg-dark-800"
                :class="platformTextClass(model.platform)"
              >
                <PlatformIcon :platform="model.platform" size="sm" />
              </div>
              <div class="min-w-0">
                <span class="font-bold text-gray-900 dark:text-white truncate block max-w-[200px]" :title="model.name">
                  {{ model.name }}
                </span>
                <span v-if="model.bestMultiplier != null && model.bestMultiplier < 1" class="text-[10px] text-rose-500 font-bold">
                  {{ formatMultiplier(model.bestMultiplier) }}
                </span>
              </div>
            </div>
          </td>

          <!-- 提供商 -->
          <td class="px-3 py-3">
            <span class="inline-flex items-center rounded-md bg-gray-100 px-2 py-0.5 text-[11px] font-medium text-gray-700 dark:bg-dark-800 dark:text-gray-300">
              {{ platformLabel(model.platform) }}
            </span>
          </td>

          <!-- 上下文 -->
          <td class="px-3 py-3 font-mono text-gray-800 dark:text-gray-200">
            {{ model.contextWindow || '128K' }}
          </td>

          <!-- 能力特性 -->
          <td class="px-3 py-3">
            <div class="flex flex-wrap gap-1 max-w-xs">
              <span
                v-for="cap in (model.capabilities || []).slice(0, 3)"
                :key="cap"
                class="inline-flex items-center gap-1 rounded bg-gray-100/80 px-1.5 py-0.5 text-[10px] text-gray-600 dark:bg-dark-800 dark:text-dark-300"
                :title="capabilityMeta(cap).description"
              >
                <Icon :name="capabilityMeta(cap).icon as any" size="xs" />
                <span>{{ capabilityMeta(cap).label }}</span>
              </span>
              <span
                v-if="(model.capabilities || []).length > 3"
                class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-400 dark:bg-dark-800"
              >
                +{{ model.capabilities!.length - 3 }}
              </span>
            </div>
          </td>

          <!-- 输入价格 -->
          <td class="px-3 py-3 font-mono font-bold text-emerald-600 dark:text-emerald-400">
            {{ formatPriceDisplay(model.minInputPrice) }}
          </td>

          <!-- 输出价格 -->
          <td class="px-3 py-3 font-mono font-bold text-blue-600 dark:text-blue-400">
            {{ formatPriceDisplay(model.minOutputPrice) }}
          </td>

          <!-- 可用渠道数 -->
          <td class="px-3 py-3 font-mono">
            <span class="rounded-md bg-gray-100 px-2 py-0.5 text-xs text-gray-700 dark:bg-dark-800 dark:text-gray-300">
              {{ model.channels.length }} 个
            </span>
          </td>

          <!-- 操作 -->
          <td class="py-3 pl-3 pr-4 text-right">
            <div class="flex items-center justify-end gap-2">
              <button
                type="button"
                class="rounded-lg p-1.5 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-800 dark:hover:text-gray-200"
                title="复制模型名称"
                @click="copyModel(model.name)"
              >
                <Icon name="copy" size="xs" />
              </button>
              <button
                type="button"
                class="rounded-lg bg-indigo-50 px-2.5 py-1 text-xs font-bold text-indigo-600 hover:bg-indigo-100 dark:bg-indigo-950/40 dark:text-indigo-400 dark:hover:bg-indigo-900/50"
                @click="$emit('view-details', model)"
              >
                查看定价
              </button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { platformLabel, platformTextClass } from '@/utils/platformColors'
import { capabilityMeta } from '../utils/capabilities'
import { formatMultiplier, formatTokenPrice } from '../utils/pricing'
import type { ModelSquareModel } from '../types'

interface Props {
  models: ModelSquareModel[]
  userGroupRates: Record<number, number>
}

defineProps<Props>()

defineEmits<{
  'view-details': [model: ModelSquareModel]
}>()

function formatPriceDisplay(val?: number | null) {
  if (val == null) return '免费 / 未设'
  return formatTokenPrice(val)
}

function copyModel(name: string) {
  navigator.clipboard.writeText(name)
}
</script>
