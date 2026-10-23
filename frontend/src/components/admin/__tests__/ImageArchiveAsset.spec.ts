import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ImageArchiveAsset from '../ImageArchiveAsset.vue'
import type { ImageArchiveFile } from '@/api/admin/imageArchive'

const mocks = vi.hoisted(() => ({ file: vi.fn(), create: vi.fn(), revoke: vi.fn() }))
vi.mock('@/api/admin/imageArchive', () => ({ imageArchiveAPI: { file: mocks.file } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const file: ImageArchiveFile = { id: 1, role: 'result', mime_type: 'image/png', width: 100, height: 100, byte_size: 12, thumbnail: true, sha256: 'test' }
beforeEach(() => {
  vi.resetAllMocks()
  vi.stubGlobal('URL', { createObjectURL: mocks.create.mockReturnValue('blob:archive-test'), revokeObjectURL: mocks.revoke })
})
afterEach(() => vi.unstubAllGlobals())

describe('authenticated archive images', () => {
  it('loads a thumbnail through the authenticated API and releases its object URL', async () => {
    const blob = new Blob(['test'], { type: 'image/png' })
    mocks.file.mockResolvedValue(blob)
    const wrapper = mount(ImageArchiveAsset, { props: { recordID: 'record', file } })
    await flushPromises()
    expect(mocks.file).toHaveBeenCalledWith('record', 1, true, expect.any(AbortSignal))
    expect(wrapper.get('img').attributes('src')).toBe('blob:archive-test')
    wrapper.unmount()
    expect(mocks.revoke).toHaveBeenCalledWith('blob:archive-test')
  })
  it('discards stale downloads when a different image is selected', async () => {
    const completions: Array<(value: Blob) => void> = []
    mocks.file.mockImplementation(() => new Promise(resolve => completions.push(resolve)))
    const wrapper = mount(ImageArchiveAsset, { props: { recordID: 'record', file, full: true } })
    expect(wrapper.get('div').classes()).toContain('aspect-square')
    expect(wrapper.get('div').classes()).not.toContain('h-full')
    const firstSignal = mocks.file.mock.calls[0][3] as AbortSignal
    await wrapper.setProps({ file: { ...file, id: 2 } })
    expect(firstSignal.aborted).toBe(true)
    completions[0](new Blob(['old']))
    await flushPromises()
    expect(wrapper.find('img').exists()).toBe(false)
    completions[1](new Blob(['current']))
    await flushPromises()
    expect(mocks.create).toHaveBeenCalledTimes(1)
    expect(mocks.file.mock.calls[1].slice(0, 3)).toEqual(['record', 2, false])
    wrapper.unmount()
  })
  it('shows an error without using public image URLs, and avoids loading large originals in the grid', async () => {
    mocks.file.mockRejectedValue(new Error('expired'))
    const wrapper = mount(ImageArchiveAsset, { props: { recordID: 'record', file } })
    await flushPromises()
    expect(wrapper.text()).toContain('imageArchive.imageFailed')
    expect(wrapper.find('img').exists()).toBe(false)
    await wrapper.setProps({ file: { ...file, thumbnail: false, id: 3 } })
    expect(mocks.file).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('imageArchive.noThumbnail')
    wrapper.unmount()
  })
})
