<template>
  <div
    v-if="model"
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
    @click.self="$emit('close')"
  >
    <div
      class="relative flex max-h-[90vh] w-full max-w-3xl flex-col overflow-hidden rounded-3xl border border-gray-200 bg-white shadow-2xl dark:border-dark-700 dark:bg-dark-900"
    >
      <!-- 弹窗顶栏 -->
      <div class="flex items-center justify-between border-b border-gray-100 px-6 py-4 dark:border-dark-800">
        <div class="flex items-center gap-3">
          <div
            class="flex h-10 w-10 items-center justify-center rounded-xl bg-gray-100 dark:bg-dark-800"
            :class="platformTextClass(model.platform)"
          >
            <PlatformIcon :platform="model.platform" size="md" />
          </div>
          <div>
            <h2 class="text-lg font-bold text-gray-900 dark:text-white">{{ model.name }}</h2>
            <p class="text-xs text-gray-500 dark:text-dark-400">
              {{ platformLabel(model.platform) }} · {{ model.contextWindow || '128K' }} 上下文
            </p>
          </div>
        </div>

        <button
          type="button"
          class="rounded-xl p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-800 dark:hover:text-gray-200"
          @click="$emit('close')"
        >
          <Icon name="x" size="sm" />
        </button>
      </div>

      <!-- 选项卡切换：渠道定价对比 vs API 调用代码 -->
      <div class="flex border-b border-gray-100 px-6 dark:border-dark-800 bg-gray-50/50 dark:bg-dark-950/40">
        <button
          type="button"
          class="border-b-2 px-4 py-3 text-xs font-bold transition-all"
          :class="
            activeTab === 'pricing'
              ? 'border-indigo-600 text-indigo-600 dark:border-indigo-400 dark:text-indigo-400'
              : 'border-transparent text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-gray-200'
          "
          @click="activeTab = 'pricing'"
        >
          全渠道定价与倍率
        </button>
        <button
          type="button"
          class="border-b-2 px-4 py-3 text-xs font-bold transition-all"
          :class="
            activeTab === 'curl'
              ? 'border-indigo-600 text-indigo-600 dark:border-indigo-400 dark:text-indigo-400'
              : 'border-transparent text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-gray-200'
          "
          @click="activeTab = 'curl'"
        >
          API 快速调用示例
        </button>
      </div>

      <!-- 弹窗内容区域 -->
      <div class="overflow-y-auto p-6 space-y-6 flex-1">
        <!-- 渠道定价面板 -->
        <div v-if="activeTab === 'pricing'" class="space-y-4">
          <div
            v-for="channel in model.channels"
            :key="channel.key"
            class="rounded-2xl border border-gray-200/90 bg-gray-50/50 p-4 dark:border-dark-700/80 dark:bg-dark-950/30"
          >
            <!-- 渠道名称与计费模式 -->
            <div class="flex items-center justify-between mb-3">
              <div class="flex items-center gap-2">
                <span class="text-sm font-bold text-gray-900 dark:text-white">{{ channel.name }}</span>
                <span class="rounded bg-indigo-50 px-2 py-0.5 text-[10px] font-bold text-indigo-600 dark:bg-indigo-950/50 dark:text-indigo-400">
                  {{ billingModeLabel(channel.pricing) }}
                </span>
              </div>
              <span class="font-mono text-xs text-gray-400">{{ channel.key }}</span>
            </div>

            <!-- 价格网格 -->
            <div class="grid grid-cols-3 sm:grid-cols-4 gap-2 text-xs mb-3">
              <div class="rounded-lg bg-white p-2 border border-gray-100 dark:bg-dark-900 dark:border-dark-800">
                <span class="text-[10px] text-gray-400 block font-bold uppercase">输入</span>
                <span class="font-mono font-bold text-emerald-600 dark:text-emerald-400">{{ formatTokenPrice(channel.pricing?.input_price) }}</span>
              </div>
              <div class="rounded-lg bg-white p-2 border border-gray-100 dark:bg-dark-900 dark:border-dark-800">
                <span class="text-[10px] text-gray-400 block font-bold uppercase">输出</span>
                <span class="font-mono font-bold text-blue-600 dark:text-blue-400">{{ formatTokenPrice(channel.pricing?.output_price) }}</span>
              </div>
              <div class="rounded-lg bg-white p-2 border border-gray-100 dark:bg-dark-900 dark:border-dark-800">
                <span class="text-[10px] text-gray-400 block font-bold uppercase">缓存写入</span>
                <span class="font-mono font-bold text-gray-700 dark:text-gray-300">{{ formatTokenPrice(channel.pricing?.cache_write_price) }}</span>
              </div>
              <div class="rounded-lg bg-white p-2 border border-gray-100 dark:bg-dark-900 dark:border-dark-800">
                <span class="text-[10px] text-gray-400 block font-bold uppercase">缓存读取</span>
                <span class="font-mono font-bold text-gray-700 dark:text-gray-300">{{ formatTokenPrice(channel.pricing?.cache_read_price) }}</span>
              </div>
            </div>

            <!-- 支持分组与用户专属倍率 -->
            <div class="space-y-1.5 pt-2 border-t border-gray-200/60 dark:border-dark-800">
              <span class="text-[11px] font-bold text-gray-400 uppercase tracking-wider block">接入分组与倍率</span>
              <div class="flex flex-wrap gap-2">
                <div
                  v-for="entry in channel.entries"
                  :key="entryKey(entry)"
                  class="flex items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-2.5 py-1 text-xs dark:border-dark-700 dark:bg-dark-900"
                >
                  <span class="font-medium text-gray-800 dark:text-gray-200">{{ entry.group.name }}</span>
                  <span class="font-mono text-[11px] text-gray-400">
                    倍率 {{ entry.group.rate_multiplier }}
                    <template v-if="userGroupRates[entry.group.id] != null">
                      · 专属 <span class="text-amber-500 font-bold">{{ userGroupRates[entry.group.id] }}</span>
                    </template>
                  </span>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- API 代码调用示例 -->
        <div v-else class="space-y-4">
          <div class="flex items-center justify-between">
            <span class="text-xs text-gray-500 dark:text-dark-400">使用标准 OpenAI SDK 或 cURL 直接接入该模型：</span>
            <button
              type="button"
              class="inline-flex items-center gap-1 rounded-lg bg-indigo-50 px-2.5 py-1 text-xs font-bold text-indigo-600 hover:bg-indigo-100 dark:bg-indigo-950/50 dark:text-indigo-400"
              @click="copySnippet"
            >
              <Icon :name="copied ? 'check' : 'copy'" size="xs" />
              <span>{{ copied ? '已复制' : '复制命令' }}</span>
            </button>
          </div>

          <pre
            class="overflow-x-auto rounded-2xl bg-gray-900 p-4 font-mono text-xs text-gray-200 dark:bg-dark-950 border border-gray-800"
          ><code>{{ curlSnippet }}</code></pre>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { platformLabel, platformTextClass } from '@/utils/platformColors'
import { entryKey } from '../utils/key'
import { billingModeLabel, formatTokenPrice } from '../utils/pricing'
import type { ModelSquareModel } from '../types'

interface Props {
  model: ModelSquareModel | null
  userGroupRates: Record<number, number>
}

const props = defineProps<Props>()
defineEmits<{
  close: []
}>()

const activeTab = ref<'pricing' | 'curl'>('pricing')
const copied = ref(false)

const curlSnippet = computed(() => {
  const modelName = props.model?.name || 'gpt-4o'
  const origin = window.location.origin
  return `curl ${origin}/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer YOUR_API_KEY" \\
  -d '{
    "model": "${modelName}",
    "messages": [
      {
        "role": "user",
        "content": "Hello world!"
      }
    ]
  }'`
})

function copySnippet() {
  navigator.clipboard.writeText(curlSnippet.value)
  copied.value = true
  setTimeout(() => {
    copied.value = false
  }, 2000)
}
</script>
