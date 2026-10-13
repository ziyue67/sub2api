import { describe, expect, it } from 'vitest'
import { formatUsageCacheRate, showUsageCacheRate, usageCacheRate, usageCacheRateParts } from '../usageCacheRate'

describe('showUsageCacheRate', () => {
  it('shows the rate to admins by default and hides it from users', () => {
    expect(showUsageCacheRate(undefined, 'admin')).toBe(true)
    expect(showUsageCacheRate({}, 'user')).toBe(false)
  })

  it('hides the rate everywhere when the feature switch is off', () => {
    expect(showUsageCacheRate({ usage_show_cache_rate: false, usage_show_cache_rate_to_users: true }, 'admin')).toBe(false)
    expect(showUsageCacheRate({ usage_show_cache_rate: false }, 'user')).toBe(false)
  })

  it('shows the rate to users only when that switch is on', () => {
    expect(showUsageCacheRate({ usage_show_cache_rate_to_users: true }, 'user')).toBe(true)
    expect(showUsageCacheRate({ usage_show_cache_rate: true, usage_show_cache_rate_to_users: false }, 'user')).toBe(false)
  })
})


describe('usageCacheRate', () => {
  it('uses cache read over input, cache read, and cache write', () => {
    const row = { input_tokens: 40_000, cache_read_tokens: 278_272, cache_creation_tokens: 1_728 }
    expect(usageCacheRate(row)).toBeCloseTo(278_272 / 320_000, 12)
    expect(formatUsageCacheRate(row)).toBe('87.0%')
  })

  it('keeps a cache write in the denominator', () => {
    const row = { input_tokens: 100, cache_read_tokens: 100, cache_creation_tokens: 100 }
    expect(usageCacheRate(row)).toBeCloseTo(1 / 3, 12)
    expect(formatUsageCacheRate(row)).toBe('33.3%')
  })

  it('shows an exact zero when the request has prompt tokens but no cache hit', () => {
    const row = { input_tokens: 1_200, cache_read_tokens: 0, cache_creation_tokens: 0 }
    expect(usageCacheRate(row)).toBe(0)
    expect(formatUsageCacheRate(row)).toBe('0%')
  })

  it('shows 100% when the whole prompt was read from cache', () => {
    expect(formatUsageCacheRate({ input_tokens: 0, cache_read_tokens: 173_306, cache_creation_tokens: 0 })).toBe('100%')
  })

  it('matches the usage-record example of input 4057, read 278272, write 4', () => {
    expect(formatUsageCacheRate({ input_tokens: 4057, cache_read_tokens: 278272, cache_creation_tokens: 4 })).toBe('98.6%')
  })

  it('returns null when there are no prompt tokens or the buckets are invalid', () => {
    expect(usageCacheRate(null)).toBeNull()
    expect(usageCacheRate(undefined)).toBeNull()
    expect(usageCacheRate({})).toBeNull()
    expect(usageCacheRate({ input_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0 })).toBeNull()
    expect(usageCacheRate({ input_tokens: -1, cache_read_tokens: 10, cache_creation_tokens: 0 })).toBeNull()
    expect(usageCacheRate({ input_tokens: Number.NaN, cache_read_tokens: 10 })).toBeNull()
    expect(formatUsageCacheRate({ input_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0 })).toBeNull()
  })

  it('treats a missing bucket as zero and keeps a tiny positive rate visible', () => {
    expect(usageCacheRateParts({ cache_read_tokens: 1 })?.rate).toBe(1)
    expect(formatUsageCacheRate({ input_tokens: 10_000, cache_read_tokens: 1, cache_creation_tokens: 0 })).toBe('<0.1%')
  })
})
