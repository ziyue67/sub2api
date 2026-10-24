import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ImageArchiveView from '../ImageArchiveView.vue'
import ImageArchiveAsset from '@/components/admin/ImageArchiveAsset.vue'

const mocks = vi.hoisted(() => ({ list: vi.fn(), stats: vi.fn(), get: vi.fn(), remove: vi.fn(), configure: vi.fn() }))
vi.mock('@/api/admin/imageArchive', () => ({ imageArchiveAPI: mocks }))
vi.mock('@/api/admin/groups', () => ({ getAllIncludingInactive: async () => [] }))
vi.mock('@/api/admin/accounts', () => ({ list: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, te: () => false }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/components/admin/ImageArchiveAsset.vue', () => ({ default: { props: ['closeOnBackdrop'], template: '<div>image</div>' } }))
vi.mock('@/components/common/BaseDialog.vue', () => ({ default: {
  props: ['show', 'title'], template: '<section v-if="show" :data-title="title"><slot /></section>',
} }))

const files = [1, 2].map(id => ({ id, role: 'result', width: 100, height: 100, byte_size: 20, mime_type: 'image/png', thumbnail: true }))
const record = { id: 'archive-test', model: 'image', prompt: 'sample prompt', files, user_id: 1, status: 'saved', created_at: '2026-10-24T00:00:00Z' }
beforeEach(() => {
  vi.resetAllMocks()
  mocks.list.mockResolvedValue({ items: [structuredClone(record)], total: 1 })
  mocks.get.mockImplementation(async () => structuredClone(record))
  mocks.stats.mockResolvedValue({ config: { enabled: false, retention_days: 30, max_bytes: 1024, account_ids: [], group_ids: [] }, stored_bytes: 40, disk_free_bytes: 1000, storage_available: true })
  mocks.remove.mockResolvedValue(undefined)
})

function button(wrapper: ReturnType<typeof mount>, label: string) {
  const found = wrapper.findAll('button').find(item => item.text() === label)
  if (!found) throw new Error(`Missing button ${label}`)
  return found
}

describe('image archive deletion', () => {
  it('saves the independent archive/viewer options without changing the selected scopes', async () => {
    const stats = { config: { enabled: true, preview_only: false, close_on_backdrop: false, retention_days: 30, max_bytes: 20 * 2 ** 30, account_ids: [7], group_ids: [8] }, stored_bytes: 40, disk_free_bytes: 1000, storage_available: true }
    mocks.stats.mockResolvedValue(stats)
    mocks.configure.mockImplementation(async config => ({ ...stats, config }))
    mocks.get.mockResolvedValue({ ...record, files: [{ ...files[0], preview_only: true }] })
    const wrapper = mount(ImageArchiveView)
    await flushPromises()
    await button(wrapper, 'imageArchive.settings').trigger('click')
    const settings = wrapper.get('[data-title="imageArchive.settings"]')
    for (const label of ['imageArchive.previewOnly', 'imageArchive.closeOnBackdrop']) {
      await settings.findAll('label').find(item => item.text() === label)!.get('input').setValue(true)
    }
    await settings.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.configure).toHaveBeenCalledWith(expect.objectContaining({ preview_only: true, close_on_backdrop: true, account_ids: [7], group_ids: [8], max_bytes: 20 * 2 ** 30 }))
    await wrapper.get('button.card').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-title="imageArchive.detail"]').text()).toContain('imageArchive.previewOnlyFile')
    expect(wrapper.findAllComponents(ImageArchiveAsset).some(component => component.props('closeOnBackdrop') === true)).toBe(true)
    wrapper.unmount()
  })

  it('requires confirmation and deletes only the selected file, keeping the other image', async () => {
    const wrapper = mount(ImageArchiveView)
    await flushPromises()
    await wrapper.get('button.card').trigger('click')
    await flushPromises()
    await button(wrapper, 'imageArchive.deleteImage').trigger('click')
    expect(mocks.remove).not.toHaveBeenCalled()
    await button(wrapper, 'common.cancel').trigger('click')
    expect(mocks.remove).not.toHaveBeenCalled()
    await button(wrapper, 'imageArchive.deleteImage').trigger('click')
    await button(wrapper, 'common.delete').trigger('click')
    await flushPromises()
    expect(mocks.remove).toHaveBeenCalledWith('archive-test', 1)
    const detail = wrapper.get('[data-title="imageArchive.detail"]')
    expect(detail.text()).toContain('imageArchive.roles.result 2')
    expect(detail.text()).not.toContain('imageArchive.roles.result 1')
    expect(mocks.stats).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('keeps the dialog on failure and can retry deleting the entire archive', async () => {
    const wrapper = mount(ImageArchiveView)
    await flushPromises()
    await wrapper.get('button.card').trigger('click')
    await flushPromises()
    await button(wrapper, 'imageArchive.deleteRecord').trigger('click')
    mocks.remove.mockRejectedValueOnce(new Error('database unavailable'))
    await button(wrapper, 'common.delete').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('database unavailable')
    expect(wrapper.find('[data-title="imageArchive.detail"]').exists()).toBe(true)
    mocks.list.mockResolvedValue({ items: [], total: 0 })
    await button(wrapper, 'common.delete').trigger('click')
    await flushPromises()
    expect(mocks.remove).toHaveBeenLastCalledWith('archive-test', undefined)
    expect(wrapper.find('[data-title="imageArchive.detail"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('imageArchive.empty')
    wrapper.unmount()
  })
})
