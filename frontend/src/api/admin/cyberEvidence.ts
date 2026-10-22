import { apiClient } from '../client'

export interface CyberEvidence {
  log_id: number
  user_id?: number
  request_id: string
  endpoint: string
  model: string
  occurred_at: string
  status: string
  expires_at?: string
  payload?: {
    input: string
    upstream_reason: string
    session_id?: string
    complete: boolean
    truncated: boolean
    omission?: string
    original_bytes: number
    redaction_version: string
    source: string
    upstream_http_status?: number
  }
}
export interface EvidenceConfig { enabled: boolean; retention_days: number }
export interface EvidenceRuntime {
  encryption_ready: boolean
  queue_depth: number
  queue_capacity: number
  queued: number
  saved: number
  failed: number
  dropped: number
  status_write_failed: number
  cleanup_failed: number
  config_load_failed: number
}
const base = '/admin/risk-control'
export async function getEvidence(id: number): Promise<CyberEvidence> {
  return (await apiClient.get<CyberEvidence>(`${base}/logs/${id}/evidence`)).data
}
export async function exportEvidence(id: number): Promise<Blob> {
  return (await apiClient.get<Blob>(`${base}/logs/${id}/evidence/export`, { responseType: 'blob' })).data
}
export async function deleteEvidence(id: number): Promise<void> {
  await apiClient.delete(`${base}/logs/${id}/evidence`)
}
export async function getEvidenceConfig(): Promise<{ config: EvidenceConfig; runtime: EvidenceRuntime }> {
  return (await apiClient.get<{ config: EvidenceConfig; runtime: EvidenceRuntime }>(`${base}/evidence/config`)).data
}
export async function updateEvidenceConfig(config: EvidenceConfig): Promise<EvidenceConfig> {
  return (await apiClient.put<EvidenceConfig>(`${base}/evidence/config`, config)).data
}
