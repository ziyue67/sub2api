export interface OAuthModelMappingRule {
  from: string
  to: string
}

export function defaultOAuthModelMappings(platform: string): OAuthModelMappingRule[] {
  return platform === 'openai' ? [{ from: 'gpt-5.4', to: 'gpt-5.5' }] : []
}

export function oauthModelMappingsError(rules: OAuthModelMappingRule[]): string | null {
  if (rules.length > 100) return 'autoConfig.mapping.tooMany'
  const seen = new Set<string>()
  const encoder = new TextEncoder()
  for (const rule of rules) {
    const from = rule.from.trim(), to = rule.to.trim()
    const invalidName = (value: string) => Array.from(value).some(char => {
      const code = char.codePointAt(0)!
      return char.trim() === '' || code < 32 || (code >= 127 && code <= 159)
    })
    const sourcePrefix = from.endsWith('*') ? from.slice(0, -1) : from
    if (!from || !to || encoder.encode(from).length > 256 || encoder.encode(to).length > 256 || invalidName(from + to) || sourcePrefix.includes('*') || to.includes('*')) return 'autoConfig.mapping.invalid'
    if (seen.has(from)) return 'autoConfig.mapping.duplicate'
    seen.add(from)
  }
  return null
}

export function oauthModelMappingsPayload(rules: OAuthModelMappingRule[]): OAuthModelMappingRule[] {
  return rules.map(rule => ({ from: rule.from.trim(), to: rule.to.trim() }))
}
