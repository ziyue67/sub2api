import { apiClient } from '../client'

export interface AstraGatewaySettings {
  account_scheduling?: boolean
  scheduling_mode?: 'account' | 'model' | 'groups'
  scheduling_group_ids?: number[]
  cookie_pool: { node_cooldown_seconds?: number; rotate_nodes?: boolean; max_node_attempts?: number; ip_affinity?: boolean; ttl_seconds?: number; enabled: boolean; source_account_ids: number[]; target_account_ids: number[] }
  ws_session: { ttl_seconds?: number; enabled: boolean; account_ids: number[] }
  revision: string
}
export function normalizeAstraGateway(value: AstraGatewaySettings): AstraGatewaySettings {
  return {
    account_scheduling: value.account_scheduling ?? false,
    scheduling_mode: value.scheduling_mode || 'account',
    scheduling_group_ids: [...(value.scheduling_group_ids || [])],
    cookie_pool: {
      enabled: value.cookie_pool.enabled,
      ip_affinity: value.cookie_pool.ip_affinity ?? false,
      rotate_nodes: value.cookie_pool.rotate_nodes ?? false,
      max_node_attempts: value.cookie_pool.max_node_attempts || 3,
      node_cooldown_seconds: value.cookie_pool.node_cooldown_seconds || 3600,
      ttl_seconds: value.cookie_pool.ttl_seconds || 230,
      source_account_ids: [...(value.cookie_pool.source_account_ids || [])],
      target_account_ids: [...(value.cookie_pool.target_account_ids || [])]
    },
    ws_session: { ttl_seconds: value.ws_session.ttl_seconds || 3600, enabled: value.ws_session.enabled, account_ids: [...(value.ws_session.account_ids || [])] },
    revision: value.revision || ''
  }
}
export async function getAstraGateway(): Promise<AstraGatewaySettings> {
  const { data } = await apiClient.get<AstraGatewaySettings>('/admin/settings/astra-routing')
  return normalizeAstraGateway(data)
}
export async function saveAstraGateway(value: AstraGatewaySettings): Promise<AstraGatewaySettings> {
  const { data } = await apiClient.put<AstraGatewaySettings>('/admin/settings/astra-routing', value)
  return normalizeAstraGateway(data)
}

export interface AstraRouteStatus {
  account_id: number; state: string; reason: string; proxy_node?: string; proxy_country?: string; checked_at?: string
  expires_at?: string; remaining_seconds: number; gateway?: string; active: boolean; answer?: string
}
export interface AstraWSStatus {
  account_id: number; ready: boolean; reason: string; active_sessions: number
  expires_at?: string; remaining_seconds: number
}
export interface AstraTestResult {
  action: string; account_id: number; success: boolean; reason: string
  duration_ms: number; checked_at: string; output_chars: number; test_kind?: string; answer?: string; expected?: string; html?: string
}
export interface AstraGatewayRuntime {
  scheduling_records?: { checked_at: string; account_id: number; schedulable: boolean; reason: string; mode?: string }[]
  cooldowns?: { account_id: number; gateway: string; retry_at: string; remaining_seconds: number }[]
  gateways?: AstraGatewayObservation[]; unknown_gateway_samples?: number
  setup?: { revision: string; state: string; phase: string; account_id: number; reason: string; started_at: string; finished_at?: string }
  generated_at: string; revision: string; sources: AstraRouteStatus[]; targets: AstraRouteStatus[]
  ws: AstraWSStatus[]; ready_routes: number; preparing: boolean; last_test?: AstraTestResult
}
export interface AstraGatewayObservation {
  gateway: string; source_account_ids: number[]; samples: number; repeated_hits: number
  source_passes: number; source_failures: number; target_passes: number; target_failures: number; last_seen: string
}
export interface AstraGatewayHistoryRecord {
  gateway: string; source_account_id: number; target_account_id: number; passes: number; failures: number
  first_seen: string; last_seen: string; last_pass?: string; last_failure?: string; last_answer: string; last_reason: string
}
export interface AstraGatewayHistoryPage { items: AstraGatewayHistoryRecord[]; total: number; unique_gateways: number }
export async function getAstraGatewayHistory(host = '', passed = true, page = 1): Promise<AstraGatewayHistoryPage> {
  return (await apiClient.get<AstraGatewayHistoryPage>('/admin/accounts/astra-gateway/history', { params: { host, passed, page } })).data
}
export async function getAstraGatewayRuntime(): Promise<AstraGatewayRuntime> {
  return (await apiClient.get<AstraGatewayRuntime>('/admin/accounts/astra-gateway/status')).data
}
export async function testAstraGateway(action: 'prepare' | 'verify' | 'ws', account_id = 0, test_kind = 'state_probe'): Promise<AstraTestResult> {
  return (await apiClient.post<AstraTestResult>('/admin/accounts/astra-gateway/test', { action, account_id, test_kind }, { timeout: 305000 })).data
}

export function resolveAstraDependencies(value: AstraGatewaySettings): AstraGatewaySettings {
  const next = normalizeAstraGateway(value)
  if (next.cookie_pool.rotate_nodes) next.cookie_pool.ip_affinity = true
  if (next.ws_session.enabled) {
    next.cookie_pool.enabled = true
    next.cookie_pool.target_account_ids = [...new Set([...next.cookie_pool.target_account_ids, ...next.ws_session.account_ids.filter(id => !next.cookie_pool.source_account_ids.includes(id))])]
  }
  return next
}
