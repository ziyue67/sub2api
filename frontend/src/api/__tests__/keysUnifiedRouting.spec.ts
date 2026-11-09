import { beforeEach, describe, expect, it, vi } from 'vitest'
import { create, update } from '../keys'
import { apiClient } from '../client'
import type { ApiKeyGroupRouting } from '@/types'

vi.mock('../client', () => ({ apiClient: { post: vi.fn(), put: vi.fn() } }))
const routing: ApiKeyGroupRouting = { strategy: 'lowest_rate', failover: true, routes: [{ group_id: 1, priority: 1, weight: 1, enabled: true, cooldown_seconds: 60 }] }
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(apiClient.post).mockResolvedValue({ data: {} })
  vi.mocked(apiClient.put).mockResolvedValue({ data: {} })
})
describe('canonical routing payload', () => {
  it('never mixes legacy routing fields into a canonical create', async () => {
    await create('new', 2, undefined, undefined, undefined, undefined, undefined, undefined, 0, [], [2], routing)
    expect(apiClient.post).toHaveBeenCalledWith('/keys', { name: 'new', concurrency_limit: 0, group_routing: routing })
  })
  it('leaves routing absent on name-only edits and sends one policy on conversion', async () => {
    await update(7, { name: 'renamed' })
    expect(apiClient.put).toHaveBeenLastCalledWith('/keys/7', { name: 'renamed' })
    await update(7, { group_routing: routing })
    expect(apiClient.put).toHaveBeenLastCalledWith('/keys/7', { group_routing: routing })
  })
})
