import { describe, expect, it } from 'vitest'
import { applyAccountRPMSettings } from '../accountRpm'

describe('shared account RPM settings', () => {
  it('preserves Anthropic strategy and buffer with the existing default limit', () => {
    const extra: Record<string, unknown> = { unrelated: true }
    applyAccountRPMSettings(extra, {
      enabled: true, baseRpm: null, strategy: 'sticky_exempt', stickyBuffer: 5
    })
    expect(extra).toEqual({ unrelated: true, base_rpm: 15, rpm_strategy: 'sticky_exempt', rpm_sticky_buffer: 5 })
  })

  it('clears soft-limit options for strict accounts', () => {
    const extra: Record<string, unknown> = { rpm_strategy: 'sticky_exempt', rpm_sticky_buffer: 50 }
    applyAccountRPMSettings(extra, { enabled: true, baseRpm: 20, strict: true })
    expect(extra).toEqual({ base_rpm: 20 })
  })

  it('removes disabled settings in a replacement and clears them explicitly in a merge', () => {
    const extra: Record<string, unknown> = { base_rpm: 20, rpm_strategy: 'tiered', rpm_sticky_buffer: 4 }
    const patch: Record<string, unknown> = {}
    applyAccountRPMSettings(extra, { enabled: false, baseRpm: 20 })
    applyAccountRPMSettings(patch, { enabled: false, baseRpm: 20 }, 'merge')
    expect(extra).toEqual({})
    expect(patch).toEqual({ base_rpm: 0, rpm_strategy: '', rpm_sticky_buffer: 0 })
  })

  it('keeps an unspecified Anthropic buffer in bulk edits but clears it for strict mode', () => {
    const extra: Record<string, unknown> = {}
    applyAccountRPMSettings(extra, { enabled: true, baseRpm: 20, stickyBuffer: null }, 'merge')
    expect(extra).not.toHaveProperty('rpm_sticky_buffer')
    applyAccountRPMSettings(extra, { enabled: true, baseRpm: 20, strict: true }, 'merge')
    expect(extra).toEqual({ base_rpm: 20, rpm_strategy: '', rpm_sticky_buffer: 0 })
  })
})
