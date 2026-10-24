import { apiClient } from '../client'

export interface ImageArchiveConfig {
  enabled: boolean
  account_ids: number[]
  group_ids: number[]
  retention_days: number
  max_bytes: number
}

export interface ImageArchiveFile {
  id: number
  role: 'result' | 'reference' | 'mask'
  mime_type: string
  byte_size: number
  sha256: string
  width: number
  height: number
  thumbnail: boolean
  revised_prompt?: string
}

export interface ImageArchiveRecord {
  id: string
  user_id: number
  api_key_id: number
  group_id: number | null
  account_id: number
  user_email: string
  api_key_name: string
  group_name: string
  account_name: string
  request_id: string
  model: string
  upstream_model: string
  endpoint: string
  prompt: string
  parameters: Record<string, unknown>
  files: ImageArchiveFile[]
  status: 'saved' | 'partial' | 'failed'
  error_codes: string
  byte_size: number
  created_at: string
  expires_at: string
}

export interface ImageArchiveStats {
  config: ImageArchiveConfig
  stored_bytes: number
  disk_free_bytes: number
  memory_bytes: number
  active: number
  saved: number
  failed: number
  dropped: number
  warning: string
  warning_at: string | null
  storage_available: boolean
}

export interface ImageArchiveFilters {
  page: number
  page_size: number
  user_id?: number
  group_id?: number
  account_id?: number
  model?: string
  from?: string
  to?: string
}

export const imageArchiveAPI = {
  async list(params: ImageArchiveFilters, signal?: AbortSignal) {
    const { data } = await apiClient.get<{ items: ImageArchiveRecord[]; total: number }>(
      '/admin/image-archive', { params, signal },
    )
    return data
  },
  async stats() {
    const { data } = await apiClient.get<ImageArchiveStats>('/admin/image-archive/stats')
    return data
  },
  async configure(config: ImageArchiveConfig) {
    const { data } = await apiClient.put<ImageArchiveStats>('/admin/image-archive/config', config)
    return data
  },
  async get(id: string, signal?: AbortSignal) {
    const { data } = await apiClient.get<ImageArchiveRecord>(`/admin/image-archive/${id}`, { signal })
    return data
  },
  async remove(id: string, fileID?: number) {
    await apiClient.delete(fileID === undefined ? `/admin/image-archive/${id}` : `/admin/image-archive/${id}/files/${fileID}`)
  },
  async file(id: string, fileID: number, thumbnail: boolean, signal?: AbortSignal) {
    const { data } = await apiClient.get<Blob>(`/admin/image-archive/${id}/files/${fileID}`, {
      params: { thumbnail }, responseType: 'blob', signal,
    })
    return data
  },
}
