import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import Panel from '../HarvestPolicyPanel.vue'
const { getSettings, updateSettings, getGroups, showSuccess } = vi.hoisted(() => ({ getSettings: vi.fn(), updateSettings: vi.fn(), getGroups: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/settings', () => ({ getSettings, updateSettings }))
vi.mock('@/api/admin/groups', () => ({ getAll: getGroups }))
vi.mock('../HarvestPoolSchedulingPanel.vue', () => ({ default: { template: '<section data-testid="pool-scheduling-panel" />' } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh-CN' } }) }))
enableAutoUnmount(afterEach)
const baseSettingsResponse = {
  openai_codex_ticket_enabled: false,
  openai_codex_ticket_fail_closed: false,
  openai_codex_ticket_models: ['gpt-6-astra', 'gpt-5.6-sol'],
  openai_codex_ticket_strategy: 'standby',
  openai_codex_ticket_strict_response: false,
  openai_codex_ticket_harvest_scope: { mode: 'all', group_ids: [], account_policy: 'schedulable_only' },
  openai_codex_ticket_harvest_proxy_url: '',
  openai_codex_ticket_harvest_proxy_configured: false,
}
beforeEach(() => {
  vi.clearAllMocks()
  getSettings.mockReset().mockResolvedValue({ ...baseSettingsResponse })
  getGroups.mockReset().mockResolvedValue([])
  updateSettings.mockReset().mockImplementation(async body => {
    const old = await getSettings.mock.results[0]?.value ?? baseSettingsResponse
    const savedStatic = old.openai_codex_ticket_static_proxy_url || (old.openai_codex_ticket_harvest_proxy_url !== 'ippool://active' ? old.openai_codex_ticket_harvest_proxy_url : '')
    return { ...old, ...body, openai_codex_ticket_static_proxy_url: savedStatic }
  })
})
function mountView() {
  return mount(Panel, { global: { stubs: {
    RouterLink: { template: '<a><slot /></a>' },
    Toggle: { props: ['modelValue'], emits: ['update:modelValue'], template: `<input type="checkbox" :checked="modelValue" @change="$emit('update:modelValue', $event.target.checked)" />` }
  } } })
}

describe('HarvestPolicyPanel', () => {
  it("submits the Codex ticket harvest toggle", async () => {
    getSettings.mockResolvedValueOnce({
      ...baseSettingsResponse,
      openai_codex_ticket_enabled: false,
    });
    const wrapper = mountView();
    await flushPromises();
    const toggle = wrapper.get("#codex-ticket-enabled");
    await toggle.setValue(true);
    await wrapper.find("form").trigger("submit.prevent");
    await flushPromises();
    expect(updateSettings.mock.calls[0]?.[0].openai_codex_ticket_enabled).toBe(true);
    expect(updateSettings.mock.calls[0]?.[0]).not.toHaveProperty("openai_codex_ticket_harvest_proxy_url");
    wrapper.unmount();
  });

  it("loads and saves independent Codex ticket model toggles", async () => {
    getSettings.mockResolvedValueOnce({
      ...baseSettingsResponse,
      openai_codex_ticket_models: ["gpt-6-astra", "gpt-5.6-sol"],
    });
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get("#codex-ticket-model-sol").setValue(false);
    await wrapper.find("form").trigger("submit.prevent");
    await flushPromises();
    expect(updateSettings.mock.calls[0]?.[0].openai_codex_ticket_models).toEqual([
      "gpt-6-astra",
    ]);
    wrapper.unmount();
  });

  it("keeps missing-ticket account pausing off by default and saves explicit opt-in", async () => {
    getSettings.mockResolvedValueOnce({
      ...baseSettingsResponse,
      openai_codex_ticket_fail_closed: false,
    });
    const wrapper = mountView();
    await flushPromises();
    const toggle = wrapper.get<HTMLInputElement>("#codex-ticket-fail-closed");
    expect(toggle.element.checked).toBe(false);
    await toggle.setValue(true);
    await wrapper.find("form").trigger("submit.prevent");
    await flushPromises();
    expect(updateSettings.mock.calls[0]?.[0].openai_codex_ticket_fail_closed).toBe(true);
    wrapper.unmount();
  });

  it("loads and changes the ticket refresh strategy", async () => {
    getSettings.mockResolvedValueOnce({...baseSettingsResponse,openai_codex_ticket_strategy:'standby'});
    const wrapper=mountView();await flushPromises();
    await wrapper.get('#codex-ticket-strategy').setValue('fixed');
    await wrapper.find('form').trigger('submit.prevent');await flushPromises();
    expect(updateSettings.mock.calls[0]?.[0].openai_codex_ticket_strategy).toBe('fixed');
    wrapper.unmount();
  });

  it('selects OAuth accounts using the existing account controls, without groups', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="harvest-policy-panel"] [data-testid="pool-scheduling-panel"]').exists()).toBe(true)
    expect(getGroups).not.toHaveBeenCalled()
    expect(wrapper.find('#codex-ticket-account-policy').exists()).toBe(false)
    await wrapper.get('#codex-ticket-harvest-scope').setValue('selected_accounts')
    expect(wrapper.text()).toContain('门票账号')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()
    expect(updateSettings.mock.calls[0]?.[0].openai_codex_ticket_harvest_scope).toEqual({
      mode: 'selected_accounts', group_ids: [], account_policy: 'prioritize_schedulable',
    })
    expect(updateSettings.mock.calls[0]?.[0]).not.toHaveProperty('astra_scheduling')
  })

  it('preserves the legacy scope until an account scope is explicitly selected', async () => {
    const legacy = { mode: 'selected', group_ids: [24], account_policy: 'schedulable_only' }
    getSettings.mockResolvedValueOnce({ ...baseSettingsResponse, openai_codex_ticket_harvest_scope: legacy })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('当前仍使用旧分组范围')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()
    expect(updateSettings.mock.calls[0]?.[0].openai_codex_ticket_harvest_scope).toEqual(legacy)
    await wrapper.get('#codex-ticket-harvest-scope').setValue('all')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()
    expect(updateSettings.mock.calls[1]?.[0].openai_codex_ticket_harvest_scope).toEqual({
      mode: 'all', group_ids: [], account_policy: 'prioritize_schedulable',
    })
  })

  it("keeps strict ticket response rejection opt-in", async () => {
    const wrapper=mountView();await flushPromises();
    expect(wrapper.get<HTMLInputElement>('#codex-ticket-strict').element.checked).toBe(false);
    await wrapper.get('#codex-ticket-strict').setValue(true);
    await wrapper.find('form').trigger('submit.prevent');await flushPromises();
    expect(updateSettings.mock.calls[0]?.[0].openai_codex_ticket_strict_response).toBe(true);
    wrapper.unmount();
  });

  it('does not save default values after loading fails', async () => {
    getSettings.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(updateSettings).not.toHaveBeenCalled()
  })
})

it('defaults beta candy observation off and persists both toggle states', async () => {
  const wrapper = mountView()
  await flushPromises()
  const toggle = wrapper.get<HTMLInputElement>('#codex-ticket-candy-observation')
  expect(toggle.element.checked).toBe(false)
  expect(wrapper.text()).toContain('启用糖果测试验证使用后的票据')
  expect(wrapper.text()).toContain('结果不影响调度、票据或账号状态')
  await toggle.setValue(true)
  await wrapper.find('form').trigger('submit.prevent')
  await flushPromises()
  expect(updateSettings.mock.calls[0][0].openai_codex_ticket_candy_observation).toBe(true)
  await toggle.setValue(false)
  await wrapper.find('form').trigger('submit.prevent')
  await flushPromises()
  expect(updateSettings.mock.calls[1][0].openai_codex_ticket_candy_observation).toBe(false)
})
