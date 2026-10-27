export interface ModelBillingRule {
  model: string
  multiplier: number
  group_ids?: number[] | null
}

export interface ModelBillingConfig {
  enabled: boolean
  rules: ModelBillingRule[]
}

export function defaultModelBillingConfig(): ModelBillingConfig {
  return { enabled: false, rules: [{ model: 'gpt-6-luna*', multiplier: 10, group_ids: [] }] }
}

export function modelBillingConfigError(config: ModelBillingConfig): string | null {
  if (config.rules.length > 100 || (config.enabled && !config.rules.length)) return 'autoConfig.modelBilling.invalid'
  const seen = new Set<string>()
  for (const rule of config.rules) {
    const model = rule.model.trim().toLowerCase()
    if (!/^[a-z0-9_./:-]+\*?$/.test(model) || model.length > 200 || seen.has(model) ||
      !Number.isFinite(rule.multiplier) || rule.multiplier < 1 || rule.multiplier > 1000) {
      return 'autoConfig.modelBilling.invalid'
    }
    if (rule.group_ids != null && (!Array.isArray(rule.group_ids) ||
      rule.group_ids.some(id => !Number.isSafeInteger(id) || id <= 0))) return 'autoConfig.modelBilling.invalidGroups'
    seen.add(model)
  }
  return null
}

export function modelBillingPayload(config: ModelBillingConfig): ModelBillingConfig {
  return { enabled: config.enabled, rules: config.rules.map(rule => ({ ...rule, model: rule.model.trim(), group_ids: [...new Set(rule.group_ids ?? [])] })) }
}
