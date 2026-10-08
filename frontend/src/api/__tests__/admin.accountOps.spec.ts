import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getAccountOpsThresholdAccounts, testAccountOpsWebhook } from '../admin/accountOps'
const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../client', () => ({ apiClient: client }))
beforeEach(() => vi.resetAllMocks())
describe('account operations notification endpoints', () => {
  it('loads monetary and percentage threshold candidates from the full account endpoint', async () => {
    const items = [{ account_id: 1, type: 'oauth', usage_windows: [] }]
    client.get.mockResolvedValue({ data: { items } })
    expect(await getAccountOpsThresholdAccounts()).toEqual(items)
    expect(client.get).toHaveBeenCalledWith('/admin/account-ops/threshold-accounts')
  })
  it('tests saved robot configuration using an encoded identifier and no credential request body', async () => {
    client.post.mockResolvedValue({ data: { ok: true } })
    expect(await testAccountOpsWebhook('bot/a')).toEqual({ ok: true })
    expect(client.post).toHaveBeenCalledWith('/admin/account-ops/webhooks/bot%2Fa/test')
  })
})
