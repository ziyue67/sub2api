import { computed, onMounted, ref } from 'vue'
import modelSquareAPI, { type ModelSquareEntry } from '@/api/modelSquare'
import userGroupsAPI from '@/api/groups'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { ModelSquareChannel, ModelSquareModel } from '../types'

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
