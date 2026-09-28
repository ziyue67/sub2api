import { beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import PriorityAccountBatch from '@/components/admin/operations/PriorityAccountBatch.vue'
import { list, bulkUpdate } from '@/api/admin/accounts'
const state = vi.hoisted(() => ({ auth: null as any }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => state.auth }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/accounts', () => ({ list: vi.fn(), bulkUpdate: vi.fn() }))
const accounts = [1, 2].map(id => ({ id, name: `account-${id}`, platform: 'openai', type: 'oauth', credentials: { plan_type: 'team' }, priority: 10, concurrency: 5 }))
beforeEach(() => { vi.resetAllMocks(); state.auth = reactive({ user: { id: 1, role: 'admin' } }); vi.mocked(list).mockResolvedValue({ items: accounts, total: 2, pages: 1 } as any) })
describe('priority batch editor', () => {
  it('retains only failed accounts for retry and preserves unselected fields', async () => {
    vi.mocked(bulkUpdate).mockResolvedValueOnce({ success: 1, failed: 1, results: [{ account_id: 1, success: true }, { account_id: 2, success: false }] })
    const wrapper = mount(PriorityAccountBatch); await wrapper.get('[data-testid="load-accounts"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="apply-accounts"]').trigger('click'); await flushPromises()
    expect(bulkUpdate).toHaveBeenCalledWith({ account_ids: [1, 2], priority: 1 })
    expect(wrapper.text()).not.toContain('account-1'); expect(wrapper.text()).toContain('account-2')
    expect(wrapper.get('[role="alert"]').text()).toContain('priorityScheduling.batch.partial')
    vi.mocked(bulkUpdate).mockResolvedValueOnce({ success: 1, failed: 0, results: [{ account_id: 2, success: true }] })
    await wrapper.get('[data-testid="apply-accounts"]').trigger('click'); await flushPromises()
    expect(bulkUpdate).toHaveBeenLastCalledWith({ account_ids: [2], priority: 1 }); wrapper.unmount()
  })
  it('invalidates loaded IDs when group scope changes', async () => {
    const wrapper = mount(PriorityAccountBatch); await wrapper.get('[data-testid="load-accounts"]').trigger('click'); await flushPromises()
    await wrapper.get('input[type="number"]').setValue('9'); expect(wrapper.find('[data-testid="apply-accounts"]').exists()).toBe(false); wrapper.unmount()
  })
})
