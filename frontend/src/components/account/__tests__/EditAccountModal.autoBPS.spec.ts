import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defaultQualityBPS } from '@/utils/qualityRulePatch'

enableAutoUnmount(afterEach)

const mocks = vi.hoisted(() => ({
  updateAccount: vi.fn(),
  listByAccount: vi.fn(),
  createPlan: vi.fn(),
  updatePlan: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: mocks.showError, showSuccess: vi.fn(), showInfo: vi.fn(), showWarning: mocks.showWarning })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSimpleMode: true })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getManagementCapabilities: vi.fn().mockResolvedValue({ web_search_enabled: false, account_quota_notify_enabled: false }),
      update: mocks.updateAccount,
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false })
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: { list: vi.fn().mockResolvedValue([]) },
    scheduledTests: { listByAccount: mocks.listByAccount, create: mocks.createPlan, update: mocks.updatePlan }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

import EditAccountModal from '../EditAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

function buildOAuthAccount(patch: Record<string, unknown> = {}) {
  return {
    id: 7, name: 'OpenAI OAuth', notes: '', platform: 'openai', type: 'oauth',
    credentials: { access_token: 'oauth-token', chatgpt_account_id: 'acc' }, extra: {},
    proxy_id: null, concurrency: 1, priority: 1, rate_multiplier: 1, status: 'active',
    group_ids: [], expires_at: null, auto_pause_on_expired: false, parent_account_id: null,
    ...patch
  } as any
}

function buildRule(patch: Record<string, unknown> = {}) {
  return {
    id: 31, account_id: 7, model_id: 'gpt-6-astra', cron_expression: '*/30 * * * *', enabled: true, max_results: 100,
    auto_recover: false, last_run_at: null, next_run_at: null, created_at: '', updated_at: '',
    pelican_config: { question_kind: 'state_probe', prompt: '', quality: {
      expected_answer: '', action: 'enable_bps', remove_group_ids: [], auto_restore: true, bps: { ...defaultQualityBPS(), models: [], all_models: true } } },
    ...patch
  }
}

function mountModal(account = buildOAuthAccount()) {
  return mount(EditAccountModal, {
    props: { show: true, account, proxies: [], groups: [] },
    global: {
      stubs: { BaseDialog: BaseDialogStub, Select: true, Icon: true, ProxySelector: true, GroupSelector: true, ModelWhitelistSelector: true }
    }
  })
}

async function submit(wrapper: ReturnType<typeof mountModal>) {
  await wrapper.get('form#edit-account-form').trigger('submit.prevent')
  await flushPromises()
}

const toggleSelector = '[data-testid="account-auto-bps-toggle"]'

describe('EditAccountModal auto BPS switch', () => {
  beforeEach(() => {
    Object.values(mocks).forEach(mock => mock.mockReset())
    mocks.updateAccount.mockImplementation(async (_id: number, payload: Record<string, unknown>) => ({ ...buildOAuthAccount(), ...payload }))
    mocks.listByAccount.mockResolvedValue([])
    mocks.createPlan.mockImplementation(async (request: Record<string, unknown>) => ({ ...buildRule(), ...request, id: 40 }))
    mocks.updatePlan.mockImplementation(async (id: number, request: Record<string, unknown>) => ({ ...buildRule(), ...request, id }))
  })

  it('shows the running rule and pauses it when switched off', async () => {
    mocks.listByAccount.mockResolvedValue([buildRule({ id: 30, pelican_config: { question_kind: 'pelican' } }), buildRule()])
    const wrapper = mountModal()
    await flushPromises()
    expect(mocks.listByAccount).toHaveBeenCalledWith(7)
    const toggle = wrapper.get(toggleSelector)
    expect(toggle.attributes('aria-checked')).toBe('true')
    expect(wrapper.find('[data-testid="account-auto-bps-pause-hint"]').exists()).toBe(true)
    expect(wrapper.get<HTMLInputElement>('[data-testid="quality-bps-auto-disable"]').element.checked).toBe(true)

    await toggle.trigger('click')
    expect(wrapper.find('[data-testid="quality-bps-settings"]').exists()).toBe(false)
    await submit(wrapper)
    expect(mocks.updateAccount).toHaveBeenCalledTimes(1)
    expect(mocks.updatePlan).toHaveBeenCalledWith(31, { enabled: false })
    expect(mocks.createPlan).not.toHaveBeenCalled()
    expect(wrapper.emitted('updated')).toHaveLength(1)
  })

  it.each([true, false])('creates BPS alongside a group/scheduling rule, enabled=%s', async (enabled) => {
    mocks.listByAccount.mockResolvedValue([buildRule({ enabled, pelican_config: {
      question_kind: 'state_probe', prompt: '', parallel_count: 1,
      quality: { expected_answer: '', action: 'disable_scheduling', remove_group_ids: [], auto_restore: true }
    } })])
    const wrapper = mountModal()
    await flushPromises()
    expect(wrapper.get(toggleSelector).attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-testid="account-auto-bps-conflict"]').exists()).toBe(false)
    await wrapper.get(toggleSelector).trigger('click')
    await submit(wrapper)
    expect(mocks.createPlan).toHaveBeenCalledTimes(1)
    expect(mocks.createPlan.mock.calls[0][0].pelican_config.quality.action).toBe('enable_bps')
    expect(mocks.updatePlan).not.toHaveBeenCalled()
  })

  it('edits only the BPS rule when both rule types exist', async () => {
    mocks.listByAccount.mockResolvedValue([buildRule({ id: 47, pelican_config: {
      question_kind: 'candy', quality: { action: 'remove_groups', remove_group_ids: [1] }
    } }), buildRule()])
    const wrapper = mountModal()
    await flushPromises()
    expect(wrapper.get(toggleSelector).attributes('aria-checked')).toBe('true')
    expect(wrapper.get(toggleSelector).attributes('disabled')).toBeUndefined()
    await wrapper.get(toggleSelector).trigger('click')
    await submit(wrapper)
    expect(mocks.updatePlan).toHaveBeenCalledWith(31, { enabled: false })
    expect(mocks.createPlan).not.toHaveBeenCalled()
  })

  it('leaves the rule alone when nothing about it changed', async () => {
    mocks.listByAccount.mockResolvedValue([buildRule()])
    const wrapper = mountModal()
    await flushPromises()
    await submit(wrapper)
    expect(mocks.updateAccount).toHaveBeenCalledTimes(1)
    expect(mocks.updatePlan).not.toHaveBeenCalled()
    expect(mocks.createPlan).not.toHaveBeenCalled()
  })

  it('creates a rule for the account when switched on', async () => {
    const wrapper = mountModal()
    await flushPromises()
    const toggle = wrapper.get(toggleSelector)
    expect(toggle.attributes('aria-checked')).toBe('false')
    expect(wrapper.find('[data-testid="account-auto-bps-pause-hint"]').exists()).toBe(false)
    await toggle.trigger('click')
    await wrapper.get('[data-testid="quality-bps-threshold"]').setValue(3)
    await submit(wrapper)
    expect(mocks.createPlan).toHaveBeenCalledTimes(1)
    const request = mocks.createPlan.mock.calls[0][0]
    expect(request).toMatchObject({ account_id: 7, model_id: 'gpt-6-astra', cron_expression: '*/30 * * * *', enabled: true })
    expect(request.pelican_config).toMatchObject({ question_kind: 'state_probe', quality: { action: 'enable_bps', auto_restore: true, bps: { failure_threshold: 3 } } })
    expect(mocks.updatePlan).not.toHaveBeenCalled()
  })

  it('blocks saving when the BPS trigger is empty', async () => {
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get(toggleSelector).trigger('click')
    await wrapper.get('[data-testid="quality-bps-threshold"]').setValue(0)
    await submit(wrapper)
    expect(mocks.showError).toHaveBeenCalledWith('qualityOps.bpsTriggerRequired')
    expect(mocks.updateAccount).not.toHaveBeenCalled()
    expect(mocks.createPlan).not.toHaveBeenCalled()
  })

  it('locks the switch and keeps the rule when it cannot be loaded', async () => {
    mocks.listByAccount.mockRejectedValue(new Error('boom'))
    const wrapper = mountModal()
    await flushPromises()
    expect(wrapper.get(toggleSelector).attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="account-auto-bps-load-error"]').exists()).toBe(true)
    await submit(wrapper)
    expect(mocks.updateAccount).toHaveBeenCalledTimes(1)
    expect(mocks.createPlan).not.toHaveBeenCalled()
    expect(mocks.updatePlan).not.toHaveBeenCalled()
  })

  it('still saves the account when the rule cannot be saved', async () => {
    mocks.createPlan.mockRejectedValue(new Error('rule failed'))
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get(toggleSelector).trigger('click')
    await submit(wrapper)
    expect(mocks.updateAccount).toHaveBeenCalledTimes(1)
    expect(mocks.showWarning).toHaveBeenCalledWith('admin.accounts.openai.autoBPSSaveFailed', 8000)
    expect(wrapper.emitted('updated')).toHaveLength(1)
  })

  it.each([
    ['PAT', buildOAuthAccount({ credentials: { access_token: 'pat', auth_mode: 'personalaccesstoken' } })],
    ['Agent Identity', buildOAuthAccount({ credentials: { access_token: 'x', auth_mode: 'agentIdentity' } })],
    ['spark shadow', buildOAuthAccount({ parent_account_id: 1 })],
    ['API key', buildOAuthAccount({ type: 'apikey', credentials: { api_key: 'sk-test' } })]
  ])('hides the switch for %s accounts', async (_name, account) => {
    const wrapper = mountModal(account)
    await flushPromises()
    expect(wrapper.find('[data-testid="account-auto-bps"]').exists()).toBe(false)
    expect(mocks.listByAccount).not.toHaveBeenCalled()
    await submit(wrapper)
    expect(mocks.createPlan).not.toHaveBeenCalled()
    expect(mocks.updatePlan).not.toHaveBeenCalled()
  })
})
