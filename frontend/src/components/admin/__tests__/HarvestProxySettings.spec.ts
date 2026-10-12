import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { apiClient } from '@/api/client'
import Panel from '../HarvestProxySettings.vue'
const { getSettings, updateSettings, getGroups, showSuccess } = vi.hoisted(() => ({ getSettings: vi.fn(), updateSettings: vi.fn(), getGroups: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/settings', () => ({ getSettings, updateSettings }))
vi.mock('@/api/client', () => ({ apiClient: { get: vi.fn() } }))
vi.mock('@/api/admin/groups', () => ({ getAll: getGroups }))
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

describe('HarvestProxySettings', () => {
  it("loads the masked Codex harvest proxy and submits a replacement URL", async () => {
    getSettings.mockResolvedValueOnce({
      ...baseSettingsResponse,
      openai_codex_ticket_harvest_proxy_url: "http://user:***@old.example.com:8080",
      openai_codex_ticket_harvest_proxy_configured: true,
    });
    const wrapper = mountView();
    await flushPromises();
    const input = wrapper.get<HTMLInputElement>("#codex-ticket-harvest-proxy");
    expect(input.element.value).toBe("http://user:***@old.example.com:8080");
    await input.setValue("socks5h://user:new-secret@new.example.com:1080");
    await wrapper.find("form").trigger("submit.prevent");
    await flushPromises();
    expect(updateSettings.mock.calls[0]?.[0].openai_codex_ticket_harvest_proxy_url)
      .toBe("socks5h://user:new-secret@new.example.com:1080");
    expect(updateSettings.mock.calls[0]?.[0]).not.toHaveProperty("openai_codex_ticket_harvest_proxy_configured");
    wrapper.unmount();
  });

  it("confirms Mihomo proxy selection and applies it only when settings are saved", async () => {
    const endpoint = "http://127.0.0.1:3101";
    const getStatus = vi.spyOn(apiClient, "get").mockResolvedValue({ data: {
      installed: true, supported: true, running: true, busy: false,
      nodes: 1, subscriptions: 1, phase: "running", endpoint,
    } });
    const wrapper = mountView();
    try {
      await flushPromises();
      await wrapper.get('input[name="codex-ticket-proxy-mode"][value="mihomo"]').setValue(true);
      await flushPromises();
      const button = wrapper.findAll("button").find(node => node.text() === "设为打票代理");
      expect(button).toBeDefined();
      await button!.trigger("click");
      expect(showSuccess).toHaveBeenCalledWith("admin.settings.gatewayForwarding.codexTicketProxyMihomoSelected");
      expect(updateSettings).not.toHaveBeenCalled();
      await wrapper.find("form").trigger("submit.prevent");
      await flushPromises();
      expect(updateSettings).toHaveBeenCalledWith(expect.objectContaining({
        openai_codex_ticket_harvest_proxy_url: endpoint,
      }));
    } finally {
      wrapper.unmount();
      getStatus.mockRestore();
    }
  });

  it("does not activate an unverified Mihomo endpoint just by selecting the mode", async () => {
    getSettings.mockResolvedValueOnce({
      ...baseSettingsResponse,
      openai_codex_ticket_harvest_proxy_url: "",
      openai_codex_ticket_harvest_proxy_configured: false,
    });
    const wrapper = mountView();
    await flushPromises();

    await wrapper
      .get<HTMLInputElement>('input[name="codex-ticket-proxy-mode"][value="mihomo"]')
      .setValue(true);
    await wrapper.find("form").trigger("submit.prevent");
    await flushPromises();

    expect(updateSettings).not.toHaveBeenCalled();
    expect(wrapper.get('[data-testid="save-harvest-proxy"]').attributes('disabled')).toBeDefined();
    wrapper.unmount();
  });

  it("saves the IP management pool as the harvest proxy and restores the static address when leaving it", async () => {
    getSettings.mockResolvedValueOnce({
      ...baseSettingsResponse,
      openai_codex_ticket_harvest_proxy_url: "http://user:***@old.example.com:8080",
      openai_codex_ticket_harvest_proxy_configured: true,
    });
    const wrapper = mountView();
    await flushPromises();

    await wrapper
      .get<HTMLInputElement>('input[name="codex-ticket-proxy-mode"][value="ip_pool"]')
      .setValue(true);
    expect(wrapper.find("#codex-ticket-harvest-proxy").exists()).toBe(false);
    expect(wrapper.find('[data-testid="codex-ticket-proxy-ip-pool-hint"]').exists()).toBe(true);
    await wrapper.find("form").trigger("submit.prevent");
    await flushPromises();
    expect(updateSettings.mock.calls[0]?.[0]).toEqual(expect.objectContaining({
      openai_codex_ticket_harvest_proxy_url: "ippool://active",
      openai_codex_ticket_use_saved_static_proxy: false,
    }));

    await wrapper
      .get<HTMLInputElement>('input[name="codex-ticket-proxy-mode"][value="static"]')
      .setValue(true);
    expect(wrapper.get<HTMLInputElement>("#codex-ticket-harvest-proxy").element.value)
      .toBe("http://user:***@old.example.com:8080");
    wrapper.unmount();
  });

  it("restores the IP management pool mode from saved settings", async () => {
    getSettings.mockResolvedValueOnce({
      ...baseSettingsResponse,
      openai_codex_ticket_harvest_proxy_url: "ippool://active",
      openai_codex_ticket_harvest_proxy_configured: true,
      openai_codex_ticket_static_proxy_url: "http://user:***@saved.example.com:8080",
    });
    const wrapper = mountView();
    await flushPromises();
    expect(wrapper.get<HTMLInputElement>('input[name="codex-ticket-proxy-mode"][value="ip_pool"]').element.checked)
      .toBe(true);
    await wrapper
      .get<HTMLInputElement>('input[name="codex-ticket-proxy-mode"][value="static"]')
      .setValue(true);
    expect(wrapper.get<HTMLInputElement>("#codex-ticket-harvest-proxy").element.value)
      .toBe("http://user:***@saved.example.com:8080");
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
