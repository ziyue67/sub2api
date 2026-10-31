import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'
import AccountUpstreamChannelBinding from '../AccountUpstreamChannelBinding.vue'
import UpstreamChannelSelector from '../UpstreamChannelSelector.vue'
import { readChannelBindings, bindChannelAccounts, listUpstreamChannels } from '@/api/admin/upstreamChannels'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/upstreamChannels', () => ({ readChannelBindings: vi.fn(), bindChannelAccounts: vi.fn(), listUpstreamChannels: vi.fn() }))
const channel = { id: 7, code: 'supplier', name: '渠道', remark: '共享', revision: 4, members: 2 }
const rows = (revision = 0, channel_id = 0) => [1, 2].map(account_id => ({ account_id, account_name: 'unknown URL', channel_id, revision, deleted: false, channel: channel_id ? channel : undefined }))
beforeEach(() => { vi.resetAllMocks(); vi.mocked(listUpstreamChannels).mockResolvedValue({ items: [channel], total: 1 }); vi.mocked(readChannelBindings).mockResolvedValue(rows()); vi.mocked(bindChannelAccounts).mockResolvedValue(rows(1, 7)) })
it('requires bounded explicit confirmation, binds unknown URL accounts and reads back then refreshes', async () => {
 const w = mount(AccountUpstreamChannelBinding, { props: { accountIds: [1, 2] } }); await w.get('[data-testid="open-channel-bindings"]').trigger('click'); await flushPromises()
 w.getComponent(UpstreamChannelSelector).vm.$emit('update:modelValue', 7); await flushPromises()
 expect(w.get('[data-testid="save-channel-binding"]').attributes('disabled')).toBeDefined()
 await w.get('[data-testid="confirm-channel-binding"]').setValue(true)
 vi.mocked(readChannelBindings).mockResolvedValue(rows(1, 7))
 await w.get('[data-testid="save-channel-binding"]').trigger('click'); await flushPromises()
 expect(bindChannelAccounts).toHaveBeenCalledWith(7, rows())
 expect(readChannelBindings).toHaveBeenCalledTimes(2); expect(w.emitted('saved')).toHaveLength(1)
 w.unmount(); const refreshed = mount(AccountUpstreamChannelBinding, { props: { accountIds: [1, 2] } }); await refreshed.get('[data-testid="open-channel-bindings"]').trigger('click'); await flushPromises(); expect(refreshed.text()).toContain('supplier'); refreshed.unmount()
})
it('keeps selection after 409 and failed readback, rereads revisions without overwriting the choice', async () => {
 const w = mount(AccountUpstreamChannelBinding, { props: { accountIds: [1, 2] } }); await w.get('[data-testid="open-channel-bindings"]').trigger('click'); await flushPromises()
 w.getComponent(UpstreamChannelSelector).vm.$emit('update:modelValue', 7); await flushPromises(); await w.get('[data-testid="confirm-channel-binding"]').setValue(true)
 vi.mocked(bindChannelAccounts).mockRejectedValueOnce({ status: 409 }); await w.get('[data-testid="save-channel-binding"]').trigger('click'); await flushPromises()
 expect(w.get('[data-testid="binding-error"]').text()).toContain('upstreamEntities.conflict'); expect(w.getComponent(UpstreamChannelSelector).props('modelValue')).toBe(7)
 vi.mocked(readChannelBindings).mockResolvedValue(rows(2, 0)); await w.get('[data-testid="reload-channel-bindings"]').trigger('click'); await flushPromises(); expect(w.getComponent(UpstreamChannelSelector).props('modelValue')).toBe(7)
 await w.get('[data-testid="confirm-channel-binding"]').setValue(true); vi.mocked(readChannelBindings).mockRejectedValueOnce(new Error('offline')); await w.get('[data-testid="save-channel-binding"]').trigger('click'); await flushPromises(); expect(w.emitted('saved')).toBeUndefined(); expect(w.get('[data-testid="binding-error"]').exists()).toBe(true); w.unmount()
})
it('does not allow unbounded filtered or more than 100 accounts', async () => {
 for (const ids of [[], Array.from({ length: 101 }, (_, i) => i + 1)]) { const w = mount(AccountUpstreamChannelBinding, { props: { accountIds: ids } }); expect(w.get('[data-testid="open-channel-bindings"]').attributes('disabled')).toBeDefined(); w.unmount() }
})

it('ignores old account read replies when selection changes to another account with the same count', async () => {
 let resolve: ((value: ReturnType<typeof rows>) => void) | undefined
 vi.mocked(readChannelBindings).mockImplementationOnce(() => new Promise(done => { resolve = done }))
 const w = mount(AccountUpstreamChannelBinding, { props: { accountIds: [1, 2] } }); await w.get('[data-testid="open-channel-bindings"]').trigger('click'); await flushPromises()
 await w.setProps({ accountIds: [3, 4] }); resolve!(rows()); await flushPromises()
 expect(w.find('[data-testid="save-channel-binding"]').exists()).toBe(false); expect(bindChannelAccounts).not.toHaveBeenCalled(); w.unmount()
})
