import { apiClient } from '../client'

export interface LegacyAdminKeyIPPolicy {
  enabled: boolean
  allowed_ips: string[]
  current_ip: string
}
const path = '/admin/settings/admin-api-key/ip-policy'
export async function getLegacyAdminKeyIPPolicy(): Promise<LegacyAdminKeyIPPolicy> {
  return (await apiClient.get<LegacyAdminKeyIPPolicy>(path)).data
}
export async function updateLegacyAdminKeyIPPolicy(policy: Pick<LegacyAdminKeyIPPolicy, 'enabled' | 'allowed_ips'>): Promise<LegacyAdminKeyIPPolicy> {
  return (await apiClient.put<LegacyAdminKeyIPPolicy>(path, policy)).data
}
