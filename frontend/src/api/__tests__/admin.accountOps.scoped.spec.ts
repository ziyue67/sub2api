import { beforeEach, describe, expect, it, vi } from 'vitest'
import { saveAccountOpsRule, deleteAccountOpsRule, saveAccountOpsWebhook, deleteAccountOpsWebhook, saveAccountOpsNotificationSettings } from '../admin/accountOps'
const client = vi.hoisted(() => ({ put: vi.fn(), delete: vi.fn() }))
vi.mock('../client', () => ({ apiClient: client }))
beforeEach(() => { vi.resetAllMocks(); client.put.mockResolvedValue({ data: { enabled: false } }); client.delete.mockResolvedValue({ data: { enabled: false } }) })
describe('scoped account-ops updates', () => {
  it('saves just one rule with its independent notification flags', async () => {
    const rule = { metric: 'balance' as const, enabled: true, threshold: 5, unit: 'USD', notify_alert: false, notify_recovery: true }
    await saveAccountOpsRule(12, rule)
    expect(client.put).toHaveBeenCalledWith('/admin/account-ops/rules/12', rule)
    await deleteAccountOpsRule(12, 'quota')
    expect(client.delete).toHaveBeenCalledWith('/admin/account-ops/rules/12', { params: { metric: 'quota' } })
  })
  it('changes a bot without posting rules or unrelated credentials', async () => {
    const input = { provider: 'wecom' as const, enabled: true, url: 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test-only' }
    await saveAccountOpsWebhook('bot/a', input)
    expect(client.put).toHaveBeenCalledWith('/admin/account-ops/webhooks/bot%2Fa', input)
    await deleteAccountOpsWebhook('bot/a')
    expect(client.delete).toHaveBeenCalledWith('/admin/account-ops/webhooks/bot%2Fa')
  })
  it('writes only common notification fields', async () => {
    const settings = { enabled: false, recipient: '', balance_low: true, weekly_quota: false, cooldown_minutes: 60 }
    await saveAccountOpsNotificationSettings(settings)
    expect(client.put).toHaveBeenCalledWith('/admin/account-ops/notification-settings', settings)
  })
})
