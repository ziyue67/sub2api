import type { ApiKey, ApiKeyGroupRoute, ApiKeyGroupRouting } from '@/types'

export const cloneGroupRouting = (value: ApiKeyGroupRouting): ApiKeyGroupRouting => ({
  ...value, routes: value.routes.map(route => ({ ...route }))
})

// Preview only: legacy settings are not rewritten by opening/saving the dialog.
export function legacyGroupRouting(key: ApiKey): ApiKeyGroupRouting {
  const routes = (key.group_routes ?? []).map(route => ({ ...route }))
  const ids = [...new Set([...(key.group_id ? [key.group_id] : []), ...(key.group_ids ?? [])])]
  for (const id of ids) {
    if (!routes.some(route => route.group_id === id)) {
      routes.push({ group_id: id, priority: routes.length + 1, weight: 1, enabled: true, cooldown_seconds: 0 })
    }
  }
  return { strategy: key.group_routes?.length ? 'priority_weighted' : 'lowest_rate', failover: !!key.group_routes?.length, routes }
}

export function validGroupRouting(value: ApiKeyGroupRouting): boolean {
  if (!['lowest_rate', 'priority_weighted'].includes(value.strategy) || !value.routes.length || value.routes.length > 20) return false
  const seen = new Set<number>()
  return value.routes.every((route: ApiKeyGroupRoute) => {
    if (!Number.isSafeInteger(route.group_id) || route.group_id <= 0 || seen.has(route.group_id)) return false
    seen.add(route.group_id)
    return Number.isInteger(route.priority) && route.priority >= 1 && route.priority <= 1000000 &&
      Number.isInteger(route.weight) && route.weight >= 1 && route.weight <= 1000000 &&
      Number.isInteger(route.cooldown_seconds) && route.cooldown_seconds >= 0 && route.cooldown_seconds <= 86400
  })
}
