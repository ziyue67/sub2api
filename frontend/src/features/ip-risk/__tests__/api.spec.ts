import { describe, expect, it, vi } from 'vitest'
import { apiClient } from '@/api/client'
import { ipRiskAPI } from '../api'

vi.mock('@/api/client', () => ({ apiClient: { get: vi.fn() } }))

describe('IP risk event pagination API', () => {
  it('sends pagination with all filters and preserves the response metadata', async () => {
    const result = { items: [], total: 102, page: 3, page_size: 50, pages: 3 }
    vi.mocked(apiClient.get).mockResolvedValue({ data: result })
    expect(await ipRiskAPI.events('198.51.100.1', 'login_failures', 'login', 3, 50)).toEqual(result)
    expect(apiClient.get).toHaveBeenLastCalledWith('/admin/settings/ip-risk/events', {
      params: { ip: '198.51.100.1', reason: 'login_failures', category: 'login', page: 3, page_size: 50 }
    })
  })
})
