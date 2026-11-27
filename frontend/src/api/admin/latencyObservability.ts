import { apiClient } from '../client'

export interface LatencyGroup {
  account_id?: number; model?: string
  reasoning_samples?: number; text_samples?: number; byte_samples?: number
  reasoning_first_avg_ms?: number | null; visible_text_first_avg_ms?: number | null; response_byte_first_avg_ms?: number | null
  visible_text_p50_ms?: number | null; visible_text_p90_ms?: number | null; visible_text_p99_ms?: number | null
  body_bucket: string
  cache_bucket: string
  reasoning_effort: string
  requests: number
  traces: number
  attempts: number
  upload_samples: number
  wait_samples: number
  visible_samples: number
  body_upload_avg_ms: number | null
  post_upload_first_content_wait_avg_ms: number | null
  post_upload_first_visible_wait_avg_ms: number | null
  large_body_switches: number
  failover_samples: number
  large_body_same_account_retries: number
  cost_samples: number
  extra_upload_component_ms: number | null
  extra_wait_component_ms: number | null
}
export interface LatencyReport {
  groups: LatencyGroup[]
  end_to_end_ms: number | null
  end_to_end_status: string
  end_to_end_source: string
  client_upload_ms: number | null
  client_upload_status: string
  scope: string
}
export async function getLatencyReport(ops: boolean, params: Record<string, string | number | undefined>, signal?: AbortSignal) {
  const { data } = await apiClient.get<LatencyReport>(ops ? '/admin/ops/dashboard/latency-report' : '/admin/usage/latency-report', { params, signal })
  return data
}
