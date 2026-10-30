import { apiClient } from './client'

export interface ImageHistoryFile {
  id: number
  url: string
  mime_type: string
  width: number
  height: number
  thumbnail: boolean
  preview_only: boolean
}

export interface ImageHistoryRecord {
  id: string
  request_id: string
  model: string
  status: 'succeeded' | 'failed' | 'unknown'
  delivery_status: 'returned' | 'interrupted' | 'unknown'
  message?: string
  image_count: number
  files: ImageHistoryFile[]
  billing: { status: 'charged' | 'no_charge' | 'pending' | 'unknown'; amount?: number }
  created_at: string
  expires_at: string
}

export const imageHistoryAPI = {
  async list(page: number, signal?: AbortSignal) {
    const { data } = await apiClient.get<{ items: ImageHistoryRecord[]; total: number; page: number; page_size: number }>(
      '/user/image-history', { params: { page }, signal },
    )
    return data
  },
  async get(id: string, signal?: AbortSignal) {
    const { data } = await apiClient.get<ImageHistoryRecord>(`/user/image-history/${encodeURIComponent(id)}`, { signal })
    return data
  },
  async file(id: string, fileID: number, thumbnail: boolean, signal?: AbortSignal) {
    const { data } = await apiClient.get<Blob>(`/user/image-history/${encodeURIComponent(id)}/files/${fileID}`, {
      params: { thumbnail }, responseType: 'blob', signal,
    })
    return data
  },
}
