import { apiClient } from '../client'

export interface SafetyPromptConfig {
  enabled: boolean
  signal_enabled: boolean
  retention_days: number
}

export interface SafetyPromptEvent {
  time: string
  id: string
  request_id?: string
  model?: string
  upstream_account_id?: number
  user_id?: number | null
  identity?: string
  action: string
  summary?: string
  request_sha256?: string
}

export interface SafetyPromptStats {
  persistent: boolean
  warning?: string
  count: number
  limit: number
}

export interface SafetyPromptConfigResponse {
  config: SafetyPromptConfig
  stats: SafetyPromptStats
}

export interface SafetyPromptEventsResponse {
  events: SafetyPromptEvent[]
  stats: SafetyPromptStats
}

const base = '/admin/risk-control/safety-prompt'

export async function getSafetyPromptConfig(): Promise<SafetyPromptConfigResponse> {
  return (await apiClient.get<SafetyPromptConfigResponse>(`${base}/config`)).data
}

export async function updateSafetyPromptConfig(config: SafetyPromptConfig): Promise<{ config: SafetyPromptConfig }> {
  return (await apiClient.put<{ config: SafetyPromptConfig }>(`${base}/config`, config)).data
}

export async function listSafetyPromptEvents(params: {
  from?: string
  to?: string
  search?: string
}): Promise<SafetyPromptEventsResponse> {
  return (await apiClient.get<SafetyPromptEventsResponse>(`${base}/events`, { params })).data
}
