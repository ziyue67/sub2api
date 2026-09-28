import { apiClient } from '../client'
export interface PriorityTeamsConfig {
  enabled: boolean
  cost_cny: number
  window_hours: number
  cny_per_billing_unit: number
  window_source: 'explicit' | 'expiry' | 'first_usage'
}
export const defaultPriorityTeamsConfig = (): PriorityTeamsConfig => ({ enabled: true, cost_cny: 50, window_hours: 4, cny_per_billing_unit: 1, window_source: 'first_usage' })
export interface PrioritySchedulingConfig {
  teams: PriorityTeamsConfig
  enabled: boolean
  mode: 'experience' | 'balanced' | 'profit' | 'custom'
  group_ids: number[]
  models: string[]
  window_minutes: number
  min_samples: number
  target_ttft_ms: number
  max_load_percent: number
  min_quality_percent: number
  quality_max_age_hours: number
  quality_weight: number
  latency_weight: number
  load_weight: number
  cost_weight: number
}
export interface PriorityCandidate {
  teams_recovery: null | {
    window_start: string
    window_end: string
    revenue_cny: number
    cost_cny: number
    profit_cny: number
    shortfall_cny: number
    remaining_seconds: number
    needs_recovery: boolean
    required_revenue_per_hour_cny: number
  }
  profit: number | null
  margin: number | null
  economics_source: 'usage' | 'rate' | 'unknown' | 'teams_window'
  revenue: number
  theoretical_cost: number
  profit_samples: number
  priority: number
  concurrency: number
  load_factor: number
  account_id: number
  account_name: string
  score: number
  tier: string
  reasons: string[]
  rate: number | null
  load_percent: number | null
  waiting: number
  samples: number
  p90_ttft_ms: number
  quality_passed: number
  quality_samples: number
}
export interface PrioritySnapshot {
  at: string
  model: string
  group_id: number | null
  mode: string
  history_ready: boolean
  candidates: PriorityCandidate[]
}
export async function getPriorityConfig(): Promise<PrioritySchedulingConfig> {
  return (await apiClient.get('/admin/priority-scheduling/config')).data
}
export async function savePriorityConfig(config: PrioritySchedulingConfig): Promise<PrioritySchedulingConfig> {
  return (await apiClient.put('/admin/priority-scheduling/config', config)).data
}
export async function getPrioritySnapshot(): Promise<PrioritySnapshot | null> {
  return (await apiClient.get('/admin/priority-scheduling/snapshot')).data.snapshot
}
