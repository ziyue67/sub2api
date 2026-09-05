import { computed, onMounted, ref } from 'vue'
import modelSquareAPI, { type ModelSquareEntry } from '@/api/modelSquare'
import userGroupsAPI from '@/api/groups'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { ModelSquareChannel, ModelSquareModel } from '../types'
import { inferCapabilities, inferContextWindow } from '../utils/capabilities'
import { isRequestBilling } from '../utils/pricing'

function groupChannels(entries: ModelSquareEntry[]): ModelSquareChannel[] {
  const channelsByKey = new Map<string, ModelSquareChannel>()
  for (const entry of entries) {
    const key = entry.channel_id > 0 ? 'channel:' + entry.channel_id : 'account-only'
    const existing = channelsByKey.get(key)
    if (existing) {
      existing.entries.push(entry)
    } else {
      channelsByKey.set(key, {
        key,
        name: entry.channel_name || '未关联渠道',
        entries: [entry],
        pricing: entry.pricing,
      })
    }
  }
  return Array.from(channelsByKey.values())
}

export function useModelSquare() {
  const appStore = useAppStore()

  const loading = ref(false)
  const error = ref<string | null>(null)
  const models = ref<ModelSquareEntry[]>([])
  const userGroupRates = ref<Record<number, number>>({})

  const platforms = computed(() => [
    'all',
    ...Array.from(new Set(models.value.map((item) => item.platform))).sort(),
  ])

  const modelGroups = computed<ModelSquareModel[]>(() => {
    const modelsByKey = new Map<string, ModelSquareModel>()
    for (const entry of models.value) {
      const key = entry.platform + ':' + entry.name.toLowerCase()
      const existing = modelsByKey.get(key)
      if (existing) {
        existing.entries.push(entry)
      } else {
        modelsByKey.set(key, {
          key,
          name: entry.name,
          platform: entry.platform,
          entries: [entry],
          channels: [],
        })
      }
    }
    return Array.from(modelsByKey.values())
      .map((model) => ({ ...model, channels: groupChannels(model.entries) }))
      .map((model) => {
        // Calculate pricing stats across channels and groups
        const allInputPrices: number[] = []
        const allOutputPrices: number[] = []
        const allCacheWritePrices: number[] = []
        const allCacheReadPrices: number[] = []
        const multipliers: number[] = []
        let isReq = false
        let hasIntervals = false
        let accounts = 0

        for (const entry of model.entries) {
          accounts += entry.account_count || 0
          const effectiveMultiplier = userGroupRates.value[entry.group.id] ?? entry.group.rate_multiplier
          if (effectiveMultiplier != null) {
            multipliers.push(effectiveMultiplier)
          }
          if (entry.pricing) {
            if (isRequestBilling(entry.pricing)) isReq = true
            if (entry.pricing.intervals && entry.pricing.intervals.length > 0) hasIntervals = true
            if (entry.pricing.input_price != null) allInputPrices.push(entry.pricing.input_price * (effectiveMultiplier ?? 1))
            if (entry.pricing.output_price != null) allOutputPrices.push(entry.pricing.output_price * (effectiveMultiplier ?? 1))
            if (entry.pricing.cache_write_price != null) allCacheWritePrices.push(entry.pricing.cache_write_price * (effectiveMultiplier ?? 1))
            if (entry.pricing.cache_read_price != null) allCacheReadPrices.push(entry.pricing.cache_read_price * (effectiveMultiplier ?? 1))
          }
        }

        const firstPricing = model.channels.find((c) => c.pricing != null)?.pricing
        const { tokens: contextTokens, label: contextWindow } = inferContextWindow(model.name)
        const capabilities = inferCapabilities(model.name, firstPricing)

        return {
          ...model,
          contextTokens,
          contextWindow,
          capabilities,
          minInputPrice: allInputPrices.length > 0 ? Math.min(...allInputPrices) : null,
          maxInputPrice: allInputPrices.length > 0 ? Math.max(...allInputPrices) : null,
          minOutputPrice: allOutputPrices.length > 0 ? Math.min(...allOutputPrices) : null,
          maxOutputPrice: allOutputPrices.length > 0 ? Math.max(...allOutputPrices) : null,
          minCacheWritePrice: allCacheWritePrices.length > 0 ? Math.min(...allCacheWritePrices) : null,
          minCacheReadPrice: allCacheReadPrices.length > 0 ? Math.min(...allCacheReadPrices) : null,
          isRequestBilling: isReq,
          hasIntervals,
          bestMultiplier: multipliers.length > 0 ? Math.min(...multipliers) : null,
          totalAccounts: accounts,
        }
      })
      .sort((a, b) => a.platform.localeCompare(b.platform) || a.name.localeCompare(b.name))
  })

  async function loadModels() {
    loading.value = true
    error.value = null
    try {
      models.value = await modelSquareAPI.list()
      userGroupRates.value = await userGroupsAPI.getUserGroupRates().catch(() => ({}))
    } catch (err: unknown) {
      const message = extractApiErrorMessage(err, '加载模型广场失败')
      error.value = message
      appStore.showError(message)
    } finally {
      loading.value = false
    }
  }

  onMounted(loadModels)

  return {
    loading,
    error,
    models,
    userGroupRates,
    platforms,
    modelGroups,
    loadModels,
  }
}
