import { describe, expect, it } from 'vitest'
import { ref } from 'vue'
import { useModelSquareFilters } from '../useModelSquareFilters'
import type { ModelSquareModel } from '../../types'

describe('useModelSquareFilters', () => {
  const mockModels: ModelSquareModel[] = [
    {
      key: 'openai:gpt-4o',
      name: 'gpt-4o',
      platform: 'openai',
      entries: [],
      channels: [{ key: 'c1', name: '官方渠道', entries: [], pricing: null }],
      contextTokens: 128_000,
      contextWindow: '128K',
      capabilities: ['vision', 'tool_call'],
      minInputPrice: 0.0000025, // $2.5 / 1M
      minOutputPrice: 0.00001, // $10 / 1M
      isRequestBilling: false,
      hasIntervals: false,
    },
    {
      key: 'anthropic:claude-3-5-sonnet',
      name: 'claude-3-5-sonnet',
      platform: 'anthropic',
      entries: [],
      channels: [
        { key: 'c2', name: '渠道2', entries: [], pricing: null },
        { key: 'c3', name: '渠道3', entries: [], pricing: null },
      ],
      contextTokens: 200_000,
      contextWindow: '200K',
      capabilities: ['vision', 'tool_call', 'reasoning'],
      minInputPrice: 0.000003, // $3.0 / 1M
      minOutputPrice: 0.000015, // $15 / 1M
      isRequestBilling: false,
      hasIntervals: false,
    },
    {
      key: 'google:gemini-1.5-flash',
      name: 'gemini-1.5-flash',
      platform: 'gemini',
      entries: [],
      channels: [{ key: 'c4', name: '渠道4', entries: [], pricing: null }],
      contextTokens: 1_000_000,
      contextWindow: '1M',
      capabilities: ['vision', 'audio', 'tool_call'],
      minInputPrice: 0.00000035, // $0.35 / 1M (< $1)
      minOutputPrice: 0.00000105,
      isRequestBilling: false,
      hasIntervals: false,
    },
  ]

  it('filters by search query', () => {
    const search = ref('claude')
    const { filteredModels } = useModelSquareFilters({
      modelGroups: ref(mockModels),
      search,
    })
    expect(filteredModels.value.length).toBe(1)
    expect(filteredModels.value[0].name).toBe('claude-3-5-sonnet')
  })

  it('filters by capability', () => {
    const search = ref('')
    const { filteredModels, toggleCapability } = useModelSquareFilters({
      modelGroups: ref(mockModels),
      search,
    })
    toggleCapability('reasoning')
    expect(filteredModels.value.length).toBe(1)
    expect(filteredModels.value[0].name).toBe('claude-3-5-sonnet')
  })

  it('filters by price range (< $1)', () => {
    const search = ref('')
    const { filteredModels, priceRange } = useModelSquareFilters({
      modelGroups: ref(mockModels),
      search,
    })
    priceRange.value = 'lt1'
    expect(filteredModels.value.length).toBe(1)
    expect(filteredModels.value[0].name).toBe('gemini-1.5-flash')
  })

  it('filters by context length (> 256K)', () => {
    const search = ref('')
    const { filteredModels, contextRange } = useModelSquareFilters({
      modelGroups: ref(mockModels),
      search,
    })
    contextRange.value = 'gt256k'
    expect(filteredModels.value.length).toBe(1)
    expect(filteredModels.value[0].name).toBe('gemini-1.5-flash')
  })

  it('sorts by channels count descending', () => {
    const search = ref('')
    const { filteredModels, sortOption } = useModelSquareFilters({
      modelGroups: ref(mockModels),
      search,
    })
    sortOption.value = 'channels_desc'
    expect(filteredModels.value[0].name).toBe('claude-3-5-sonnet')
  })

  it('resets all filters cleanly', () => {
    const search = ref('')
    const { filteredModels, priceRange, toggleCapability, resetFilters } = useModelSquareFilters({
      modelGroups: ref(mockModels),
      search,
    })
    priceRange.value = 'lt1'
    toggleCapability('vision')
    resetFilters()
    expect(priceRange.value).toBe('all')
    expect(filteredModels.value.length).toBe(3)
  })
})
