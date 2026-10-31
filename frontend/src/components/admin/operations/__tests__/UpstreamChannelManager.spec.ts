import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'
import UpstreamChannelManager from '../UpstreamChannelManager.vue'
import { listUpstreamChannels, getUpstreamChannel, saveUpstreamChannel, deleteUpstreamChannel } from '@/api/admin/upstreamChannels'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/upstreamChannels', () => ({ listUpstreamChannels: vi.fn(), getUpstreamChannel: vi.fn(), saveUpstreamChannel: vi.fn(), deleteUpstreamChannel: vi.fn(), getChannelMembers: vi.fn() }))
const entity = { id: 7, code: 'supplier-a', name: '渠道A', remark: '旧备注', revision: 3, members: 2 }
beforeEach(() => { vi.resetAllMocks(); vi.mocked(listUpstreamChannels).mockResolvedValue({ items: [entity], total: 1 }); vi.mocked(getUpstreamChannel).mockImplementation(async () => ({ ...entity })); vi.mocked(saveUpstreamChannel).mockResolvedValue({ ...entity, remark: '新备注', revision: 4 }) })
it('confirms multi-account impact, keeps immutable ID/code, saves then GETs and cancels without writes', async () => {
 const w = mount(UpstreamChannelManager, { props: { expanded: true } }); await flushPromises(); await w.get('[data-testid="edit-upstream-channel"]').trigger('click'); await flushPromises()
 expect(w.get('[data-testid="channel-code"]').attributes('disabled')).toBeDefined(); expect(w.get('[data-testid="delete-upstream-channel"]').attributes('disabled')).toBeDefined()
 await w.get('[data-testid="channel-remark"]').setValue('取消草稿')
 await w.findAll('button').find(b => b.text() === 'upstreamEntities.cancel')!.trigger('click'); await flushPromises()
 expect(saveUpstreamChannel).not.toHaveBeenCalled(); expect(w.find('[data-testid="channel-entity-draft"]').exists()).toBe(false)
 await w.get('[data-testid="edit-upstream-channel"]').trigger('click'); await flushPromises()
 expect((w.get('[data-testid="channel-remark"]').element as HTMLTextAreaElement).value).toBe('旧备注')
 await w.get('[data-testid="channel-remark"]').setValue('新备注'); expect(w.get('[data-testid="save-upstream-channel"]').attributes('disabled')).toBeDefined(); await w.get('[data-testid="confirm-channel-edit"]').setValue(true)
 vi.mocked(getUpstreamChannel).mockResolvedValue({ ...entity, remark: '新备注', revision: 4 }); await w.get('[data-testid="save-upstream-channel"]').trigger('click'); await flushPromises()
 expect(saveUpstreamChannel).toHaveBeenCalledWith(7, expect.objectContaining({ code: 'supplier-a', remark: '新备注', revision: 3, confirm: true })); expect(getUpstreamChannel).toHaveBeenCalledTimes(3); expect(w.find('[data-testid="channel-entity-draft"]').exists()).toBe(false); expect(deleteUpstreamChannel).not.toHaveBeenCalled(); w.unmount()
})
it('keeps failed drafts and validates Unicode character counts', async () => {
 const w = mount(UpstreamChannelManager, { props: { expanded: true } }); await flushPromises(); await w.get('[data-testid="new-upstream-channel"]').trigger('click'); await w.get('[data-testid="channel-code"]').setValue('supplier-new'); await w.get('[data-testid="channel-name"]').setValue('新渠道')
 await w.get('[data-testid="channel-remark"]').setValue('中'.repeat(501)); expect(w.get('[data-testid="save-upstream-channel"]').attributes('disabled')).toBeDefined()
 await w.get('[data-testid="channel-remark"]').setValue('😀'.repeat(500)); expect(w.get('[data-testid="save-upstream-channel"]').attributes('disabled')).toBeDefined(); await w.get('[data-testid="confirm-channel-edit"]').setValue(true); expect(w.get('[data-testid="save-upstream-channel"]').attributes('disabled')).toBeUndefined()
 vi.mocked(saveUpstreamChannel).mockRejectedValueOnce(new Error('offline')); await w.get('[data-testid="save-upstream-channel"]').trigger('click'); await flushPromises(); expect((w.get('[data-testid="channel-remark"]').element as HTMLTextAreaElement).value).toBe('😀'.repeat(500)); expect(w.get('[data-testid="channel-entity-error"]').exists()).toBe(true); w.unmount()
})

it('clears with an explicit empty save and reads the persisted value after remount', async () => {
 let w=mount(UpstreamChannelManager,{props:{expanded:true}});await flushPromises()
 await w.get('[data-testid="edit-upstream-channel"]').trigger('click');await flushPromises()
 await w.get('[data-testid="channel-remark"]').setValue('   ');await w.get('[data-testid="confirm-channel-edit"]').setValue(true)
 const cleared={...entity,remark:'',revision:4};vi.mocked(saveUpstreamChannel).mockResolvedValue(cleared);vi.mocked(getUpstreamChannel).mockResolvedValue(cleared);vi.mocked(listUpstreamChannels).mockResolvedValue({items:[cleared],total:1})
 await w.get('[data-testid="save-upstream-channel"]').trigger('click');await flushPromises()
 expect(saveUpstreamChannel).toHaveBeenCalledWith(7,expect.objectContaining({remark:'',revision:3,confirm:true}));expect(w.emitted('changed')).toHaveLength(1);w.unmount()
 w=mount(UpstreamChannelManager,{props:{expanded:true}});await flushPromises();expect(w.text()).toContain('upstreamEntities.noRemark')
 await w.get('[data-testid="edit-upstream-channel"]').trigger('click');await flushPromises();expect((w.get('[data-testid="channel-remark"]').element as HTMLTextAreaElement).value).toBe('');w.unmount()
})
it('retains an edited draft on GET verification failure and requires reread plus confirmation', async () => {
 const w=mount(UpstreamChannelManager,{props:{expanded:true}});await flushPromises();await w.get('[data-testid="edit-upstream-channel"]').trigger('click');await flushPromises()
 await w.get('[data-testid="channel-remark"]').setValue('新备注');await w.get('[data-testid="confirm-channel-edit"]').setValue(true)
 vi.mocked(getUpstreamChannel).mockRejectedValueOnce(new Error('readback offline'))
 await w.get('[data-testid="save-upstream-channel"]').trigger('click');await flushPromises()
 expect(w.emitted('changed')).toBeUndefined();expect(w.get('[data-testid="channel-entity-error"]').exists()).toBe(true);expect((w.get('[data-testid="channel-remark"]').element as HTMLTextAreaElement).value).toBe('新备注');expect(w.get('[data-testid="save-upstream-channel"]').attributes('disabled')).toBeDefined()
 vi.mocked(getUpstreamChannel).mockResolvedValue({...entity,remark:'别人保存的备注',revision:5})
 await w.findAll('button').find(b=>b.text()==='upstreamEntities.readLatest')!.trigger('click');await flushPromises()
 expect(w.text()).toContain('别人保存的备注');expect((w.get('[data-testid="channel-remark"]').element as HTMLTextAreaElement).value).toBe('新备注');expect(w.get('[data-testid="save-upstream-channel"]').attributes('disabled')).toBeDefined()
 await w.get('[data-testid="confirm-channel-edit"]').setValue(true);expect(w.get('[data-testid="save-upstream-channel"]').attributes('disabled')).toBeUndefined();w.unmount()
})
