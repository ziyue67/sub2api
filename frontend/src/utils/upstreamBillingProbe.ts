import type { AccountPlatform, AccountType } from '@/types'

const UPSTREAM_BILLING_PROBE_PLATFORMS = new Set<AccountPlatform>([
  'openai',
  'anthropic',
  'gemini',
  'antigravity',
  'grok'
])

export function isUpstreamBillingProbeIdentity(
  platform: AccountPlatform | string,
  accountType: AccountType | string
): boolean {
  return accountType === 'apikey' && UPSTREAM_BILLING_PROBE_PLATFORMS.has(platform as AccountPlatform)
}

export function isUpstreamBillingProbeAccount(account: {
  platform: AccountPlatform | string
  type: AccountType | string
}): boolean {
  return isUpstreamBillingProbeIdentity(account.platform, account.type)
}
