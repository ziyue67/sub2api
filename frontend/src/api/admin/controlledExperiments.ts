import { apiClient } from '../client'

export type ExperimentChannel = 'native_http' | 'native_ws' | 'prism' | 'bps'
export interface ExperimentTask {
  id: string
  family: string
  split: 'screen' | 'confirm'
  category: string
  prompt: string
  max_turns: number
  grader_kind?: string
  sql_cases?: number
}
export interface ExperimentRoute {
  account_id: number
  channel: ExperimentChannel
  account_name: string
  parent_account_id?: number
  proxy_id?: number
  mapped_model: string
}
export interface ExperimentInput {
  name: string
  split: 'screen' | 'confirm'
  task_ids: string[]
  routes: Pick<ExperimentRoute, 'account_id' | 'channel'>[]
  model: string
  reasoning_effort: string
  repetitions: number
  max_calls: number
  timeout_seconds: number
}
export interface ExperimentRun {
  id: number
  name: string
  status: string
  max_calls: number
  reserved_calls: number
  created_at: string
  started_at?: string
  finished_at?: string
  stop_reason: string
  spec: {
    suite_version: string
    model: string
    reasoning_effort: string
    repetitions: number
    timeout_seconds: number
    planned_max_calls: number
    routes: ExperimentRoute[]
    tasks?: ExperimentTask[]
  }
}
export interface ExperimentGrade {
  passed: boolean
  score: number
  reason: string
  passed_cases?: number
  total_cases?: number
  missing_records?: string[]
  rejected_calls?: number
}
export interface ExperimentDiagnostic {
  code: string
  http_status: number
  upstream_status?: number
  forwarding_status?: number
  actual_channel: string
  upstream_endpoint: string
  upstream_model: string
  response_model: string
  effective_effort: string
  effort_evidence: string
  terminal: string
  identity_stable: boolean
  request_id?: string
  response_id?: string
  duration_ms: number
  submissions: number
  output_types: string[]
  usage_source: string
  usage: { input_tokens: number; output_tokens: number; cache_read_input_tokens?: number }
  cost_usd?: number
  cost_incomplete: boolean
}
export interface ExperimentAttempt {
  sequence: number
  route_index: number
  phase: string
  task_id: string
  repetition: number
  turn: number
  status: string
  started_at: string
  answer: string
  grade?: ExperimentGrade
  diagnostic: ExperimentDiagnostic
  tool_trace?: { namespace: string; name: string; record_id: string; ok: boolean }[]
}
export interface ExperimentReport {
  run: ExperimentRun
  attempts: ExperimentAttempt[]
  preflight: { route_index: number; available: boolean; reason: string; catalog: string }[]
  routes: {
    route_index: number
    eligibility: string
    calls: number
    completed_calls: number
    protocol_failures: number
    unknown_calls: number
    graded_tasks: number
    passed_tasks: number
    mean_score: number | null
    cost_usd: number
    cost_incomplete: boolean
  }[]
  comparisons: {
    left: number; right: number; paired_tasks: number; excluded_tasks: number
    left_mean: number | null; right_mean: number | null; mean_difference: number | null
  }[]
}

export const controlledExperimentsAPI = {
  async catalog() {
    const { data } = await apiClient.get<{ version: string; seed: number; tasks: ExperimentTask[] }>('/admin/controlled-experiments/catalog')
    return data
  },
  async list(beforeId = 0) {
    const { data } = await apiClient.get<ExperimentRun[]>('/admin/controlled-experiments', { params: { before_id: beforeId } })
    return data ?? []
  },
  async create(input: ExperimentInput) {
    const { data } = await apiClient.post<ExperimentRun>('/admin/controlled-experiments', input)
    return data
  },
  async report(id: number) {
    const { data } = await apiClient.get<ExperimentReport>(`/admin/controlled-experiments/${id}`)
    return data
  },
  async start(id: number) { await apiClient.post(`/admin/controlled-experiments/${id}/start`) },
  async stop(id: number) { await apiClient.post(`/admin/controlled-experiments/${id}/stop`) }
}
