import { describe, expect, it } from 'vitest'

import {
  isUpstreamBillingProbeAccount,
  isUpstreamBillingProbeIdentity
} from '../upstreamBillingProbe'

describe('upstream billing probe eligibility', () => {
  it.each(['openai', 'anthropic', 'gemini', 'antigravity', 'grok'])(
    'accepts %s API-key accounts',
    (platform) => {
      expect(isUpstreamBillingProbeIdentity(platform, 'apikey')).toBe(true)
    }
  )

  it('rejects DeepSeek and non-API-key accounts', () => {
    expect(isUpstreamBillingProbeAccount({ platform: 'deepseek', type: 'apikey' })).toBe(false)
    expect(isUpstreamBillingProbeIdentity('openai', 'oauth')).toBe(false)
  })
})
