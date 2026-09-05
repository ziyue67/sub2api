import { computed, ref, type ComputedRef, type Ref } from 'vue'
import type { ModelSquareModel } from '../types'
import type {
  BillingTypeFilter,
  ContextRange,
  ModelCapability,
  PriceRange,
  SortOption,
  ViewMode,
} from '../types'
import { PER_MILLION_TOKENS } from '../utils/pricing'

export interface UseModelSquareFiltersOptions {
  modelGroups: ComputedRef<ModelSquareModel[]> | Ref<ModelSquareModel[]>
  search: Ref<string>
}

export function useModelSquareFilters(options: UseModelSquareFiltersOptions) {
  const platform = ref('all')
  const selectedPlatforms = ref<string[]>([])
  const selectedCapabilities = ref<ModelCapability[]>([])
  const priceRange = ref<PriceRange>('all')
  const contextRange = ref<ContextRange>('all')
  const billingType = ref<BillingTypeFilter>('all')
  const sortOption = ref<SortOption>('default')
  const viewMode = ref<ViewMode>('grid')

  const filteredModels = computed(() => {
    const query = options.search.value.trim().toLowerCase()
    let list = options.modelGroups.value.filter((model) => {
      // Platform filter: backward-compatible single platform or multiselect list
      if (selectedPlatforms.value.length > 0) {
        if (!selectedPlatforms.value.includes(model.platform)) return false
      } else if (platform.value !== 'all' && model.platform !== platform.value) {
        return false
      }

      // Capabilities filter
      if (selectedCapabilities.value.length > 0) {
        const modelCaps = model.capabilities ?? []
        const hasAllCaps = selectedCapabilities.value.every((cap) => modelCaps.includes(cap))
        if (!hasAllCaps) return false
      }

      // Price range filter (based on min input price per million tokens)
      if (priceRange.value !== 'all') {
        const minInput = model.minInputPrice != null ? model.minInputPrice * PER_MILLION_TOKENS : null
        if (priceRange.value === 'free') {
          if (minInput !== 0) return false
        } else if (priceRange.value === 'lt1') {
          if (minInput == null || minInput >= 1) return false
        } else if (priceRange.value === '1to5') {
          if (minInput == null || minInput < 1 || minInput > 5) return false
        } else if (priceRange.value === '5to15') {
          if (minInput == null || minInput < 5 || minInput > 15) return false
        } else if (priceRange.value === 'gt15') {
          if (minInput == null || minInput <= 15) return false
        }
      }

      // Context range filter
      if (contextRange.value !== 'all') {
        const tokens = model.contextTokens ?? 128_000
        if (contextRange.value === 'lt32k' && tokens >= 32_000) return false
        if (contextRange.value === '32kTo128k' && (tokens < 32_000 || tokens > 128_000)) return false
        if (contextRange.value === '128kTo256k' && (tokens < 128_000 || tokens > 256_000)) return false
        if (contextRange.value === 'gt256k' && tokens <= 256_000) return false
      }

      // Billing type filter
      if (billingType.value !== 'all') {
        if (billingType.value === 'tokens' && model.isRequestBilling) return false
        if (billingType.value === 'request' && !model.isRequestBilling) return false
        if (billingType.value === 'intervals' && !model.hasIntervals) return false
      }

      // Search query
      if (!query) return true
      return [
        model.name,
        model.platform,
        ...(model.capabilities ?? []),
        ...model.entries.flatMap((entry) => [entry.channel_name, entry.group.name]),
      ]
        .join(' ')
        .toLowerCase()
        .includes(query)
    })

    // Sort
    if (sortOption.value === 'price_asc') {
      list = [...list].sort((a, b) => (a.minInputPrice ?? 999999) - (b.minInputPrice ?? 999999))
    } else if (sortOption.value === 'price_desc') {
      list = [...list].sort((a, b) => (b.minInputPrice ?? -1) - (a.minInputPrice ?? -1))
    } else if (sortOption.value === 'output_price_asc') {
      list = [...list].sort((a, b) => (a.minOutputPrice ?? 999999) - (b.minOutputPrice ?? 999999))
    } else if (sortOption.value === 'output_price_desc') {
      list = [...list].sort((a, b) => (b.minOutputPrice ?? -1) - (a.minOutputPrice ?? -1))
    } else if (sortOption.value === 'channels_desc') {
      list = [...list].sort((a, b) => b.channels.length - a.channels.length)
    } else if (sortOption.value === 'name_asc') {
      list = [...list].sort((a, b) => a.name.localeCompare(b.name))
    }

    return list
  })

  function setPlatform(value: string) {
    platform.value = value
    if (value === 'all') {
      selectedPlatforms.value = []
    } else {
      selectedPlatforms.value = [value]
    }
  }

  function togglePlatform(p: string) {
    const idx = selectedPlatforms.value.indexOf(p)
    if (idx > -1) {
      selectedPlatforms.value.splice(idx, 1)
    } else {
      selectedPlatforms.value.push(p)
    }
  }

  function toggleCapability(cap: ModelCapability) {
    const idx = selectedCapabilities.value.indexOf(cap)
    if (idx > -1) {
      selectedCapabilities.value.splice(idx, 1)
    } else {
      selectedCapabilities.value.push(cap)
    }
  }

  function resetFilters() {
    platform.value = 'all'
    selectedPlatforms.value = []
    selectedCapabilities.value = []
    priceRange.value = 'all'
    contextRange.value = 'all'
    billingType.value = 'all'
    sortOption.value = 'default'
  }

  return {
    platform,
    selectedPlatforms,
    selectedCapabilities,
    priceRange,
    contextRange,
    billingType,
    sortOption,
    viewMode,
    filteredModels,
    setPlatform,
    togglePlatform,
    toggleCapability,
    resetFilters,
  }
}
