/** Admin API for independently schedulable account egress lanes. */

import { apiClient } from '../client'

export type AccountProxyLaneTransport = 'proxy' | 'direct'
export type AccountProxyLaneStatus = 'active' | 'paused' | 'error' | 'disabled'

export interface AccountProxyLaneProxyView {
  id: number
  name: string
  protocol: string
  host: string
  port: number
  status: string
  expires_at?: string | null
}

export interface AccountProxyLane {
  id: number
  account_id: number
  proxy_id: number | null
  name: string
  transport: AccountProxyLaneTransport
  concurrency: number
  weight: number
  priority: number
  status: AccountProxyLaneStatus
  schedulable: boolean
  cooldown_until?: string | null
  created_at: string
  updated_at: string
  proxy?: AccountProxyLaneProxyView | null
  current_concurrency?: number
}

export interface AccountProxyLanePayload {
  name: string
  proxy_id?: number | null
  transport: AccountProxyLaneTransport
  concurrency: number
  weight: number
  priority: number
  status: AccountProxyLaneStatus
  schedulable: boolean
  cooldown_until?: string | null
}

export async function list(accountId: number): Promise<AccountProxyLane[]> {
  const { data } = await apiClient.get<AccountProxyLane[]>(`/admin/accounts/${accountId}/proxy-lanes`)
  return data ?? []
}

export async function create(accountId: number, payload: AccountProxyLanePayload): Promise<AccountProxyLane> {
  const { data } = await apiClient.post<AccountProxyLane>(`/admin/accounts/${accountId}/proxy-lanes`, payload)
  return data
}

export async function update(
  accountId: number,
  laneId: number,
  payload: Partial<AccountProxyLanePayload>
): Promise<AccountProxyLane> {
  const { data } = await apiClient.put<AccountProxyLane>(
    `/admin/accounts/${accountId}/proxy-lanes/${laneId}`,
    payload
  )
  return data
}

export async function remove(accountId: number, laneId: number): Promise<void> {
  await apiClient.delete(`/admin/accounts/${accountId}/proxy-lanes/${laneId}`)
}

export const proxyLanesAPI = { list, create, update, remove }

export default proxyLanesAPI
