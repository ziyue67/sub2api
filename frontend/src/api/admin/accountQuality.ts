import { apiClient } from '../client'
import type { PelicanTestConfig, ScheduledTestPlan, ScheduledTestResult } from '@/types'
export async function listQualityPlans(): Promise<ScheduledTestPlan[]> {
  const { data } = await apiClient.get<ScheduledTestPlan[]>('/admin/account-quality-plans')
  return data ?? []
}
export async function runQualityPlan(id: number): Promise<void> {
  await apiClient.post(`/admin/account-quality-plans/${id}/run`)
}

export interface QualityOperation extends ScheduledTestResult {
  account_id: number
  account_name: string
  passed_count: number
  skipped_count?: number
  total_count: number
  result_ids: number[]
}
export async function listQualityOperations(beforeId = 0): Promise<{ items: QualityOperation[]; next_cursor: number }> {
  const { data } = await apiClient.get('/admin/account-quality-results', { params: { before_id: beforeId } })
  return { items: data.items ?? [], next_cursor: data.next_cursor ?? 0 }
}

// 「分组规则」：按账户筛选保存的规则，现在和之后匹配的账户各自得到一条规则。
// group 为空 = 全部分组，'ungrouped' = 未分组，否则是分组 id；statuses 任一命中，空 = 全部状态。
export interface QualityRuleAccountFilter {
  group?: string
  type?: string
  search?: string
  statuses?: string[]
}
export interface QualityRuleTemplate {
  id: number
  account_filter: QualityRuleAccountFilter
  model_id: string
  cron_expression: string
  enabled: boolean
  max_results: number
  pelican_config: PelicanTestConfig
  plan_ids: number[]
  last_synced_at: string | null
  created_at: string
  updated_at: string
}
export interface QualityRuleTemplateRequest {
  account_filter?: QualityRuleAccountFilter
  model_id?: string
  cron_expression?: string
  enabled?: boolean
  max_results?: number
  pelican_config?: PelicanTestConfig
}
export async function listQualityTemplates(): Promise<QualityRuleTemplate[]> {
  const { data } = await apiClient.get<QualityRuleTemplate[]>('/admin/account-quality-templates')
  return data ?? []
}
export async function createQualityTemplate(body: QualityRuleTemplateRequest): Promise<{ template: QualityRuleTemplate; created: number }> {
  const { data } = await apiClient.post('/admin/account-quality-templates', body)
  return data
}
export async function updateQualityTemplate(id: number, body: QualityRuleTemplateRequest): Promise<{ template: QualityRuleTemplate; updated: number; failed: number; created: number }> {
  const { data } = await apiClient.put(`/admin/account-quality-templates/${id}`, body)
  return data
}
export async function deleteQualityTemplate(id: number, deletePlans: boolean): Promise<{ deleted: number }> {
  const { data } = await apiClient.delete(`/admin/account-quality-templates/${id}`, { params: { delete_plans: deletePlans ? 'true' : undefined } })
  return data
}
