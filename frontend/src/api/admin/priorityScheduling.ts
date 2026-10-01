import { apiClient } from '../client'
export interface PrioritySchedulingConfig {
  balance_protocols?: boolean
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
  capacity_band?: number
  selection_weight?: number
  exploration_eligible?: boolean
  bound_groups?: number
  profit: number | null
  margin: number | null
  economics_source: 'usage' | 'rate' | 'unknown'
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
  selection_policy?: string
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
