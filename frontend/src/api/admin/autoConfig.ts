import { apiClient } from '../client'
import type { ModelBillingConfig } from '@/utils/modelBilling'
import type { ExcelBPSDefaults } from '@/utils/excelBPSDefaults'
import type { OAuthModelMappingRule } from '@/utils/oauthModelMappings'
export interface AutoConfig {
 model_billing?: ModelBillingConfig
 model_mappings?: OAuthModelMappingRule[] | null
 excel_bps?: ExcelBPSDefaults
 enabled: boolean
 platform: string
 priority: number
 load_factor: number
 concurrency: number
 group_ids: number[]
 upgrade_enabled: boolean
 upgrade_group_ids: number[]
 successes_per_step: number
 upgrade_step: number
 max_concurrency: number
 cooldown_seconds: number
 cost_multiplier?: number
 revision: string
 runtime_blocked?: boolean
}
export async function getAutoConfig(): Promise<AutoConfig> { return (await apiClient.get('/admin/account-ops/auto-config')).data }
export async function saveAutoConfig(config: AutoConfig): Promise<AutoConfig> { return (await apiClient.put('/admin/account-ops/auto-config', config)).data }

export type AutoConfigEventKind = 'config_saved' | 'initial_applied' | 'concurrency_upgraded' | 'failure_cooldown'
export interface AutoConfigEvent {
  id: number
  account_id: number
  account_name: string
  platform: string
  kind: AutoConfigEventKind
  created_at: string
  details: {
    model_mapping?: Record<string, string>
    config?: AutoConfig
    priority: number
    load_factor: number
    concurrency: number
    previous_concurrency: number
    group_ids?: number[]
    cooldown_seconds: number
  }
}
export interface AutoConfigEventsPage {
  items: AutoConfigEvent[]
  has_more: boolean
}
export async function getAutoConfigEvents(params: { before?: number; kind?: AutoConfigEventKind | ''; limit?: number } = {}): Promise<AutoConfigEventsPage> {
  return (await apiClient.get('/admin/account-ops/auto-config/events', { params })).data
}
