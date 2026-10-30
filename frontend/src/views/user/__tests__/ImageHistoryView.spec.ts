import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ImageHistoryView from '../ImageHistoryView.vue'

const mocks = vi.hoisted(() => ({ list: vi.fn(), file: vi.fn(), save: vi.fn() }))
vi.mock('@/api/imageHistory', () => ({ imageHistoryAPI: { list: mocks.list, file: mocks.file } }))
vi.mock('file-saver', () => ({ saveAs: mocks.save }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, args?: object) => `${key}${args ? JSON.stringify(args) : ''}`, locale: { value: 'en' } }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/components/common/BaseDialog.vue', () => ({ default: { props: ['show'], template: '<section v-if="show" data-testid="detail"><slot /><button data-testid="close" @click="$emit(\'close\')">Close</button></section>' } }))
vi.mock('@/components/admin/ImageArchiveAsset.vue', () => ({ default: { name: 'ImageArchiveAsset', props: ['recordID', 'file', 'loadFile'], template: '<div data-testid="asset" />' } }))

const record = {
  id: 'own-record', request_id: 'public-request', model: 'public-image', status: 'succeeded', delivery_status: 'interrupted', image_count: 1,
  created_at: '2026-10-30T12:00:00Z', expires_at: '2026-11-01T12:00:00Z', billing: { status: 'charged', amount: 0.123456 },
  files: [{ id: 2, url: '/protected', mime_type: 'image/jpeg', width: 10, height: 10, preview_only: true, thumbnail: true }],
  account_name: 'SECRET_ACCOUNT', upstream_model: 'SECRET_UPSTREAM', prompt: 'SECRET_PROMPT', error_codes: 'SECRET_ERROR',
}
beforeEach(() => { vi.resetAllMocks(); mocks.list.mockResolvedValue({ items: [record], total: 21, page: 1, page_size: 20 }) })
afterEach(() => vi.restoreAllMocks())

describe('customer API image history', () => {
  it('shows own result and payment only, uses the user file loader and downloads the saved preview', async () => {
    mocks.file.mockResolvedValue(new Blob(['image'], { type: 'image/jpeg' }))
    const wrapper = mount(ImageHistoryView)
    await flushPromises()
    expect(wrapper.text()).toContain('0.123456')
    expect(wrapper.text()).toContain('imageArchive.transferInterrupted')
    expect(wrapper.text()).not.toContain('SECRET_')
    const asset = wrapper.findComponent({ name: 'ImageArchiveAsset' })
    expect(asset.props('loadFile')).toBe(mocks.file)
    expect(asset.props('file').role).toBe('result')
    const detailButton = wrapper.findAll('button').find(button => button.text() === 'imageArchive.detail')!
    await detailButton.trigger('click')
    expect(wrapper.get('[data-testid="detail"]').text()).toContain('public-request')
    expect(wrapper.text()).toContain('imageArchive.previewOnlyFile')
    const download = wrapper.findAll('button').find(button => button.text() === 'imageArchive.downloadSaved')!
    await download.trigger('click'); await flushPromises()
    expect(mocks.file).toHaveBeenCalledWith('own-record', 2, false, expect.any(AbortSignal))
    expect(mocks.save).toHaveBeenCalledWith(expect.any(Blob), 'image-own-record-2.jpg')
    expect(wrapper.text()).not.toContain('SECRET_')
    wrapper.unmount()
  })

  it('keeps unknown billing separate from free requests and cancels stale page loads', async () => {
    mocks.list.mockResolvedValueOnce({ items: [{ ...record, files: [], status: 'failed', billing: { status: 'unknown' } }], total: 21, page: 1 })
    const wrapper = mount(ImageHistoryView); await flushPromises()
    expect(wrapper.text()).toContain('imageArchive.receipt.unknown')
    expect(wrapper.text()).not.toContain('imageArchive.receipt.no_charge')
    expect(wrapper.text()).toContain('imageArchive.generation.failed')
    const firstSignal = mocks.list.mock.calls[0][1] as AbortSignal
    const next = wrapper.findAll('button').find(button => button.text() === 'imageArchive.next')!
    await next.trigger('click'); await flushPromises()
    expect(firstSignal.aborted).toBe(true)
    expect(mocks.list).toHaveBeenLastCalledWith(2, expect.any(AbortSignal))
    const finalSignal = mocks.list.mock.calls[1][1] as AbortSignal
    wrapper.unmount(); expect(finalSignal.aborted).toBe(true)
  })

  it('shows a generic load error and does not render raw backend errors', async () => {
    mocks.list.mockRejectedValue(new Error('SECRET_INTERNAL_ERROR'))
    const wrapper = mount(ImageHistoryView); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('imageArchive.loadFailed')
    expect(wrapper.text()).not.toContain('SECRET_')
    wrapper.unmount()
  })
})
