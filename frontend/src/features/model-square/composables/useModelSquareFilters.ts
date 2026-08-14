import { computed, ref, type ComputedRef, type Ref } from 'vue'
import type { ModelSquareModel } from '../types'

export interface UseModelSquareFiltersOptions {
  modelGroups: ComputedRef<ModelSquareModel[]> | Ref<ModelSquareModel[]>
  search: Ref<string>
}

export function useModelSquareFilters(options: UseModelSquareFiltersOptions) {
  const platform = ref('all')

  const filteredModels = computed(() => {
    const query = options.search.value.trim().toLowerCase()
    return options.modelGroups.value.filter((model) => {
      if (platform.value !== 'all' && model.platform !== platform.value) return false
      if (!query) return true
      return [model.name, model.platform, ...model.entries.flatMap((entry) => [entry.channel_name, entry.group.name])]
        .join(' ')
        .toLowerCase()
        .includes(query)
    })
  })

  function setPlatform(value: string) {
    platform.value = value
  }

  return {
    platform,
    filteredModels,
    setPlatform,
  }
}
