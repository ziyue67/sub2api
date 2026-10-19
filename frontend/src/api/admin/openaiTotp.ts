import { apiClient } from '../client'

export interface TOTPRotation {
  task_id: number
  account_id: number
  state: 'queued' | 'running' | 'enrolling' | 'prepared' | 'activating' | 'verifying' | 'uncertain' | 'succeeded' | 'failed'
  action: 'rotate' | 'verify_new' | 'verify_old'
  has_candidate: boolean
  error_code?: string
  updated_at: string
}
export type TOTPExportSource = 'current' | 'candidate' | 'previous'
export interface TOTPExport {
  email: string
  password?: string
  secret: string
  otpauth_uri: string
  state: string
  source: TOTPExportSource
}
export interface OpenAISessionExportTask {
  id: number
  account_id: number
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'expired'
  stage: string
  error_code?: string
  attempt: number
  created_at: string
  updated_at: string
  finished_at?: string
}
const base = (id: number) => `/admin/accounts/${id}`
export async function getTOTPRotation(id: number): Promise<TOTPRotation | null> {
  return (await apiClient.get(`${base(id)}/totp-rotation`)).data
}
export async function rotateTOTP(id: number): Promise<TOTPRotation> {
  return (await apiClient.post(`${base(id)}/totp-rotation`)).data
}
export async function verifyTOTP(id: number, taskId: number, action: 'verify_new' | 'verify_old'): Promise<void> {
  await apiClient.post(`${base(id)}/totp-rotation/verify`, { task_id: taskId, action })
}
export async function exportTOTP(id: number, source: TOTPExportSource, includePassword: boolean): Promise<TOTPExport> {
  return (await apiClient.post(`${base(id)}/totp-export`, { source, include_password: includePassword })).data
}
export async function exportChatGPTSession(id: number): Promise<OpenAISessionExportTask> {
  return (await apiClient.post(`${base(id)}/export-session`, {})).data
}
export async function getChatGPTSessionExportTask(id: number, taskId: number): Promise<OpenAISessionExportTask> {
  return (await apiClient.get(`${base(id)}/export-session/${taskId}`)).data
}
export async function downloadChatGPTSession(id: number, taskId: number): Promise<Record<string, unknown>> {
  return (await apiClient.get(`${base(id)}/export-session/${taskId}/download`)).data
}
