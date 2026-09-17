import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { post }
}))

import { accountsAPI, probeUpstreamUsage, probeUpstreamUsageBatch } from '@/api/admin/accounts'

describe('admin account upstream usage probe API', () => {
  beforeEach(() => {
    post.mockReset()
  })

  it('uses only the dedicated read-only account and batch probe endpoints', async () => {
    const result = { account_id: 7, snapshot: { status: 'ok' } }
    post.mockResolvedValueOnce({ data: result })
    post.mockResolvedValueOnce({ data: { results: [result] } })

    await expect(probeUpstreamUsage(7)).resolves.toEqual(result)
    await expect(probeUpstreamUsageBatch([7])).resolves.toEqual([result])

    expect(post).toHaveBeenNthCalledWith(1, '/admin/accounts/7/upstream-usage-probe')
    expect(post).toHaveBeenNthCalledWith(2, '/admin/accounts/upstream-usage-probe/batch', { account_ids: [7] })
  })

  it('does not expose the unused snapshot-list request', () => {
    expect(accountsAPI).not.toHaveProperty('getUpstreamUsageSnapshots')
  })
})
