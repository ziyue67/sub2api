import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SecurityRecords from '../SecurityRecords.vue'
import * as api from '../api'

vi.mock('../api', () => ({ getSummary: vi.fn(), listEvents: vi.fn(), getEvent: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('personal security records', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(api.getSummary).mockResolvedValue({ enabled: true, observe_only: false, policy: 'session_block', threshold: 3, restricted: true, states: [{ scope: 'session', subject_key: 'hash-of-session', policy: 'session_block', count: 3, blocked_until: '2026-10-22T00:00:00Z', route_until: null, action: 'block', reason: 'jailbreak', updated_at: '2026-10-21T00:00:00Z' }] })
    vi.mocked(api.listEvents).mockResolvedValue({ page: 1, page_size: 10, total: 1, items: [{ id: 9, user_id: 7, api_key_id: 8, scope: 'session', policy: 'session_block', reason: 'jailbreak', count: 3, model: 'model', source: 'prompt_audit', created_at: '2026-10-21T00:00:00Z' }] })
  })
  it('shows restriction and cause, loading the original only on demand as text', async () => {
    vi.mocked(api.getEvent).mockResolvedValue({ id: 9, user_id: 7, api_key_id: 8, scope: 'session', policy: 'session_block', reason: 'jailbreak', count: 3, model: 'model', source: 'prompt_audit', created_at: '', full_prompt: '<script>danger()</script> original conversation' })
    const wrapper = mount(SecurityRecords)
    await flushPromises()
    expect(wrapper.get('[data-testid="restriction-status"]').text()).toBe('securityRecords.restricted')
    expect(wrapper.text()).toContain('jailbreak')
    expect(api.getEvent).not.toHaveBeenCalled()
    const expand = wrapper.findAll('button').find(button => button.text() === 'securityRecords.showPrompt')!
    await expand.trigger('click'); await flushPromises()
    expect(api.getEvent).toHaveBeenCalledWith(9, false)
    expect(wrapper.get('[data-testid="security-original-prompt"]').text()).toContain('<script>danger()</script>')
    expect(wrapper.find('script').exists()).toBe(false)
    expect(api.listEvents).toHaveBeenCalledWith(1, false, undefined)
  })
  it('reports unavailable status instead of claiming unrestricted when status loading fails', async () => {
    vi.mocked(api.getSummary).mockRejectedValue(new Error('offline'))
    const wrapper = mount(SecurityRecords); await flushPromises()
    expect(wrapper.find('[data-testid="restriction-status"]').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toBe('securityRecords.loadFailed')
  })
})
