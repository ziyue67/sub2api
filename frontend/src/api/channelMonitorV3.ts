import { apiClient } from './client'
import type { ChannelMonitorMode } from '@/utils/featureFlags'

/** Latest judged status of a component; `unknown` without traffic in the window. */
export type MonitorV3Status = 'operational' | 'degraded' | 'down' | 'unknown'
/** `insufficient`: fewer requests than the page minimum, so the slot is not judged. */
export type MonitorV3CellStatus = 'operational' | 'degraded' | 'down' | 'insufficient'
export type MonitorV3Visibility = 'group' | 'public'
export type MonitorV3Range = '24h' | '7d' | '30d'

export const MONITOR_V3_INTERVALS = [1, 2, 3, 5, 10, 15, 30, 60] as const
/** V2 latency histogram bounds; thresholds compare exactly against the P50 bucket. */
export const MONITOR_V3_TTFT_THRESHOLDS = [1000, 2000, 3000, 5000, 8000, 10000, 15000, 30000, 60000, 120000] as const
export const MONITOR_V3_RANGES: MonitorV3Range[] = ['24h', '7d', '30d']

export interface MonitorV3Cell {
  start: string
  status: MonitorV3CellStatus
  /** 0–1 over counted requests (errors users caused are not counted). */
  success_rate: number
  ttft_p50_ms?: number | null
  top_error?: string
  /** Admin-only volumes. */
  requests?: number
  errors?: number
  ignored_errors?: number
}

export interface MonitorV3ComponentStatus {
  id: number
  name: string
  description?: string
  model?: string
  multiplier?: number | null
  status: MonitorV3Status
  /** 0–100 over the configured range; null without traffic. */
  availability: number | null
  /** Admin-only request count over the range. */
  requests?: number
  last_data_at: string | null
  /** One entry per slot of the window, oldest first; null without traffic. */
  cells: Array<MonitorV3Cell | null>
  group_id?: number
}

export interface MonitorV3CategoryStatus {
  id: number
  name: string
  description?: string
  availability: number | null
  components: MonitorV3ComponentStatus[]
}

export interface MonitorV3Window {
  start: string
  end: string
  latest: boolean
  has_older: boolean
}

export interface MonitorV3StatusPage {
  generated_at: string
  interval_minutes: number
  cells: number
  availability_range: MonitorV3Range
  availability_since: string
  down_error_rate: number
  degraded_error_rate: number
  degraded_ttft_ms: number
  min_requests: number
  data_through: string | null
  footer_note: string
  window: MonitorV3Window
  featured: MonitorV3ComponentStatus | null
  categories: MonitorV3CategoryStatus[]
  open_incidents: number
}

export interface MonitorV3Incident {
  component_id: number
  component_name: string
  started_at: string
  ended_at: string | null
  down_slots: number
  top_error?: string
  requests?: number
  errors?: number
}

export interface MonitorV3IncidentPage {
  items: MonitorV3Incident[]
  total: number
  page: number
  page_size: number
  since: string
}

export interface MonitorV3Config {
  version: number
  interval_minutes: number
  cells: number
  availability_range: MonitorV3Range
  down_error_rate: number
  degraded_error_rate: number
  degraded_ttft_ms: number
  min_requests: number
  ignored_error_categories: string[]
  featured_component_id: number | null
  footer_note: string
  updated_at: string
}

export interface MonitorV3Category {
  id: number
  name: string
  description: string
  sort_order: number
  created_at: string
  updated_at: string
}

export interface MonitorV3CategoryInput {
  name: string
  description: string
}

export interface MonitorV3ComponentInput {
  category_id: number | null
  name: string
  description: string
  group_id: number
  /** Empty means every model requested in the group. */
  model: string
  /** 0 inherits the page threshold. */
  degraded_ttft_ms: number
  show_multiplier: boolean
  visibility: MonitorV3Visibility
  enabled: boolean
}

export interface MonitorV3Component extends MonitorV3ComponentInput {
  id: number
  sort_order: number
  group_name: string
  group_platform: string
  group_status: string
  group_rate_multiplier: number
  group_deleted: boolean
  created_at: string
  updated_at: string
}

export interface MonitorV3Settings {
  config: MonitorV3Config
  categories: MonitorV3Category[]
  components: MonitorV3Component[]
  error_categories: string[]
  data_through: string | null
}

const base = (admin: boolean) => (admin ? '/admin/channel-monitor-v3' : '/channel-monitor-v3')

/** `end` (unix seconds) selects an older window by its last slot. */
export async function getStatus(end: number | null, admin = false, signal?: AbortSignal) {
  const { data } = await apiClient.get<MonitorV3StatusPage>(`${base(admin)}/status`, {
    params: end ? { end } : undefined,
    signal,
  })
  return data
}

export async function getIncidents(page: number, pageSize: number, admin = false, signal?: AbortSignal) {
  const { data } = await apiClient.get<MonitorV3IncidentPage>(`${base(admin)}/incidents`, {
    params: { page, page_size: pageSize },
    signal,
  })
  return data
}

export async function getSettings() {
  const { data } = await apiClient.get<MonitorV3Settings>('/admin/channel-monitor-v3/settings')
  return data
}

export async function updateConfig(config: MonitorV3Config) {
  const { data } = await apiClient.put<MonitorV3Config>('/admin/channel-monitor-v3/config', config)
  return data
}

export async function createCategory(input: MonitorV3CategoryInput) {
  const { data } = await apiClient.post<MonitorV3Category>('/admin/channel-monitor-v3/categories', input)
  return data
}

export async function updateCategory(id: number, input: MonitorV3CategoryInput) {
  const { data } = await apiClient.put<MonitorV3Category>(`/admin/channel-monitor-v3/categories/${id}`, input)
  return data
}

export async function deleteCategory(id: number) {
  await apiClient.delete(`/admin/channel-monitor-v3/categories/${id}`)
}

export async function createComponent(input: MonitorV3ComponentInput) {
  const { data } = await apiClient.post<MonitorV3Component>('/admin/channel-monitor-v3/components', input)
  return data
}

export async function updateComponent(id: number, input: MonitorV3ComponentInput) {
  const { data } = await apiClient.put<MonitorV3Component>(`/admin/channel-monitor-v3/components/${id}`, input)
  return data
}

export async function deleteComponent(id: number) {
  await apiClient.delete(`/admin/channel-monitor-v3/components/${id}`)
}

export async function reorder(categoryIds: number[], componentIds: number[]) {
  await apiClient.post('/admin/channel-monitor-v3/reorder', { category_ids: categoryIds, component_ids: componentIds })
}

/** Switches the site between V1, V2 and V3 without a full settings save. */
export async function setMonitorMode(mode: ChannelMonitorMode) {
  const { data } = await apiClient.put<{ mode: ChannelMonitorMode }>('/admin/channel-monitor-mode', { mode })
  return data.mode
}
