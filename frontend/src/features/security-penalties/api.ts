import { apiClient } from '@/api/client'

export interface PenaltyConfig {
  enabled: boolean
  observe_only: boolean
  policy: 'session_block' | 'user_block' | 'session_route_then_block'
  threshold: number
  window_hours: number
  duration_hours: number
  target_group_id: number | null
  all_groups: boolean
  group_ids: number[]
  exempt_user_ids: number[]
  version: number
  risk_control_enabled: boolean
}
export interface PenaltyState {
  scope: string
  subject_key: string
  policy: string
  count: number
  blocked_until: string | null
  route_until: string | null
  action: string
  reason: string
  updated_at: string
}
export interface PenaltySummary {
  enabled: boolean
  observe_only: boolean
  policy: string
  threshold: number
  restricted: boolean
  states: PenaltyState[]
}
export interface PenaltyEvent {
  id: number
  user_id: number
  api_key_id: number
  scope: string
  policy: string
  reason: string
  full_prompt?: string
  count: number
  model: string
  source: string
  created_at: string
}
export interface PenaltyPage { items: PenaltyEvent[]; total: number; page: number; page_size: number }
export async function getSummary(): Promise<PenaltySummary> {
  return (await apiClient.get<PenaltySummary>('/user/security/summary')).data
}
export async function listEvents(page = 1, admin = false, userId?: number): Promise<PenaltyPage> {
  return (await apiClient.get<PenaltyPage>(admin ? '/admin/security-penalties/events' : '/user/security/events', { params: { page, page_size: 10, ...(admin && userId ? { user_id: userId } : {}) } })).data
}
export async function getEvent(id: number, admin = false): Promise<PenaltyEvent> {
  return (await apiClient.get<PenaltyEvent>(`${admin ? '/admin/security-penalties/events' : '/user/security/events'}/${id}`)).data
}
export async function getConfig(): Promise<PenaltyConfig> {
  return (await apiClient.get<PenaltyConfig>('/admin/security-penalties/config')).data
}
export async function saveConfig(config: PenaltyConfig): Promise<PenaltyConfig> {
  return (await apiClient.put<PenaltyConfig>('/admin/security-penalties/config', config)).data
}
export async function release(userId: number, reason: string): Promise<void> {
  await apiClient.post(`/admin/security-penalties/users/${userId}/release`, { reason })
}
