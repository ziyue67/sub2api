import { describe, expect, it } from 'vitest'

import {
  DEEPSEEK_WS_MODE_HTTP_BRIDGE,
  DEEPSEEK_WS_MODE_OFF,
  normalizeDeepSeekWSMode,
  resolveDeepSeekWSModeFromExtra
} from '@/utils/deepseekWsMode'

describe('deepseekWsMode', () => {
  it('accepts only modes implemented by the DeepSeek HTTP bridge', () => {
    expect(normalizeDeepSeekWSMode('off')).toBe(DEEPSEEK_WS_MODE_OFF)
    expect(normalizeDeepSeekWSMode('http_bridge')).toBe(DEEPSEEK_WS_MODE_HTTP_BRIDGE)
    expect(normalizeDeepSeekWSMode('ctx_pool')).toBeNull()
    expect(normalizeDeepSeekWSMode('passthrough')).toBeNull()
    expect(normalizeDeepSeekWSMode('dedicated')).toBeNull()
  })

  it('uses the effective global bridge state only when an account has no explicit mode', () => {
    expect(resolveDeepSeekWSModeFromExtra(undefined, false)).toBe(DEEPSEEK_WS_MODE_OFF)
    expect(resolveDeepSeekWSModeFromExtra(undefined, true)).toBe(DEEPSEEK_WS_MODE_HTTP_BRIDGE)
    expect(resolveDeepSeekWSModeFromExtra({
      deepseek_responses_websockets_v2_mode: 'off'
    }, true)).toBe(DEEPSEEK_WS_MODE_OFF)
    expect(resolveDeepSeekWSModeFromExtra({
      deepseek_responses_websockets_v2_mode: 'ctx_pool'
    }, true)).toBe(DEEPSEEK_WS_MODE_OFF)
  })
})
