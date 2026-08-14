import { ref } from 'vue'
import { useDebounceFn } from '@vueuse/core'

export function useModelSquareSearch(delay = 300) {
  const search = ref('')
  const debouncedSearch = ref('')

  const updateDebounced = useDebounceFn((value: string) => {
    debouncedSearch.value = value
  }, delay)

  function setSearch(value: string) {
    search.value = value
    updateDebounced(value)
  }

  function clearSearch() {
    search.value = ''
    debouncedSearch.value = ''
  }

  return {
    search,
    debouncedSearch,
    setSearch,
    clearSearch,
  }
}
