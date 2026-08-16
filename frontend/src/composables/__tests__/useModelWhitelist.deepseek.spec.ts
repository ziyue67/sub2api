import { describe, expect, it } from 'vitest'

import { getModelsByPlatform, getPresetMappingsByPlatform } from '../useModelWhitelist'

describe('DeepSeek model whitelist presets', () => {
  it('exposes the official harness V4 models in flash-first order', () => {
    expect(getModelsByPlatform('deepseek')).toEqual([
      'deepseek-v4-flash',
      'deepseek-v4-pro',
    ])
  })

  it('uses DeepSeek-specific presets instead of falling back to Anthropic', () => {
    expect(getPresetMappingsByPlatform('deepseek')).toEqual([
      expect.objectContaining({ from: 'deepseek-v4-flash', to: 'deepseek-v4-flash' }),
      expect.objectContaining({ from: 'deepseek-v4-pro', to: 'deepseek-v4-pro' }),
    ])
  })
})
