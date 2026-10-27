import fs from 'node:fs'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh/tokenGuardV2'
import type { TokenGuardV2Status } from '@/api/admin/accountTokenGuardV2'
import View from '../TokenGuardV2View.vue'

const api = vi.hoisted(() => ({ list: vi.fn(), probe: vi.fn() }))
vi.mock('@/api/admin/accountTokenGuardV2', () => ({
  listTokenGuardV2Accounts: api.list, probeTokenGuardV2Account: api.probe,
  createTokenGuardV2Account: vi.fn(), updateTokenGuardV2Account: vi.fn(), deleteTokenGuardV2Account: vi.fn(),
  reloginTokenGuardV2Account: vi.fn(), saveTokenGuardV2Rules: vi.fn(), saveTokenGuardV2Runtime: vi.fn(),
  updateTokenGuardV2Switches: vi.fn(), resumeTokenGuardV2Budget: vi.fn()
}))
vi.mock('@/api/admin/accounts', () => ({ list: vi.fn().mockResolvedValue({ items: [] }), default: { list: vi.fn().mockResolvedValue({ items: [] }) } }))
vi.mock('@/api/admin/groups', () => ({ getByPlatform: vi.fn().mockResolvedValue([]), default: { getByPlatform: vi.fn().mockResolvedValue([]) } }))
vi.mock('@/api/admin/proxies', () => ({ getAllWithCount: vi.fn().mockResolvedValue([]), default: { getAllWithCount: vi.fn().mockResolvedValue([]) } }))
vi.mock('@/api/admin/credentialEncryption', () => ({ getCredentialEncryption: vi.fn().mockResolvedValue({ configured: true }), initializeCredentialEncryption: vi.fn() }))

const at = '2026-10-01T01:02:03Z'
const fallback: TokenGuardV2Status = { probe_interval_seconds: 1800, retry_interval_seconds: 300, relogin_cooldown_seconds: 1800, fail_streak_threshold: 2, accounts: [{ account_id: 42, account_name: 'fixture', account_status: 'active', schedulable: true, enabled: false, auto_relogin_enabled: false, probe_state: 'transient', probe_detail: 'synthetic-private', fail_streak: 0, next_probe_at: at, last_probe_at: at, probe_failure: { category: 'account_retired', code: 'account_retired', stage: 'inspection', action: 'contact_provider', occurred_at: at }, latest_task: { id: 7, account_id: 42, status: 'failed', stage: 'failed', error: 'synthetic-private', attempt: 1, created_at: at, updated_at: at, finished_at: at, failure: { category: 'account_retired', code: 'account_retired', stage: 'terminal_result', action: 'contact_provider', occurred_at: at } } }] }
const source = process.env.CREDENTIAL_OPERATION_API_FIXTURE
if (process.env.CREDENTIAL_OPERATION_REQUIRE_SEAM && !source) throw new Error('The storage/API seam artifact is required')
const responses: { code: number; data: TokenGuardV2Status }[] = source
  ? JSON.parse(fs.readFileSync(source, 'utf8')) : [{ code: 0, data: fallback }]

describe('persisted credential failures on the existing operations page', () => {
  afterEach(() => vi.clearAllMocks())
  it.each(responses.map((response, index) => [index, response.data] as const))('renders stored API response %s and manual inspection feedback', async (_index, status) => {
    api.list.mockResolvedValue(status)
    const row = status.accounts[0]!
    api.probe.mockResolvedValue(row)
    const view = mount(View, { global: { plugins: [createI18n({ legacy: false, locale: 'zh', missingWarn: false, fallbackWarn: false, messages: { zh: { tokenGuardV2: zh } } })], stubs: { AppLayout: { template: '<div><slot /></div>' }, SmartOpsNav: true, Pagination: true, OpenAITOTPDialog: true } } })
    try {
      await flushPromises()
      expect(view.text()).toContain('账号已删除或停用')
      const taskCode = row.latest_task!.failure!.code as keyof typeof zh.operationFailure.reasons
      expect(view.text()).toContain(zh.operationFailure.reasons[taskCode])
      expect(view.text()).toContain(row.latest_task!.failure!.stage)
      expect(view.text()).toContain(new Date(row.latest_task!.finished_at!).toLocaleString())
      expect(view.text()).not.toContain('synthetic-private')
      if (row.refresh_failure) expect(view.text()).toContain('最近刷新失败尝试')
      const button = view.findAll('button').find(button => button.text() === '立即巡检')!
      await button.trigger('click')
      await flushPromises()
      expect(view.get('[role="alert"]').text()).toContain('账号已删除或停用')
      expect(view.text()).not.toContain('已完成 fixture 的即时巡检')
    } finally { view.unmount() }
  })
})
