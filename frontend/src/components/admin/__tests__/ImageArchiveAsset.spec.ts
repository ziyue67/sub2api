import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ImageArchiveAsset from '../ImageArchiveAsset.vue'
import type { ImageArchiveFile } from '@/api/admin/imageArchive'

const mocks = vi.hoisted(() => ({ file: vi.fn(), create: vi.fn(), revoke: vi.fn() }))
vi.mock('@/api/admin/imageArchive', () => ({ imageArchiveAPI: { file: mocks.file } }))
vi.mock('@/components/common/BaseDialog.vue', () => ({
  default: { props: ['show', 'contentClass'], template: '<div v-if="show" data-testid="viewer" :class="contentClass"><slot /><button data-testid="close-viewer" @click="$emit(\'close\')">Close</button></div>' },
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const file: ImageArchiveFile = { id: 1, role: 'result', mime_type: 'image/png', width: 100, height: 100, byte_size: 12, thumbnail: true, sha256: 'test' }
beforeEach(() => {
  vi.resetAllMocks()
  vi.stubGlobal('URL', { createObjectURL: mocks.create.mockReturnValue('blob:archive-test'), revokeObjectURL: mocks.revoke })
})
afterEach(() => vi.unstubAllGlobals())

describe('authenticated archive images', () => {
  it('opens the original in a viewer, zooms with buttons and wheel, resets and closes on image change', async () => {
    mocks.file.mockResolvedValue(new Blob(['image']))
    const wrapper = mount(ImageArchiveAsset, { props: { recordID: 'record', file, full: true } })
    await flushPromises()
    await wrapper.get('img').trigger('click')
    const viewer = wrapper.get('[data-testid="viewer"]')
    expect(viewer.text()).toContain('100%')
    await viewer.get('[aria-label="imageArchive.zoomIn"]').trigger('click')
    expect(viewer.text()).toContain('125%')
    await viewer.get('[data-testid="archive-viewport"]').trigger('wheel', { deltaY: -100 })
    expect(viewer.text()).toContain('138%')
    const reset = viewer.findAll('button').find(button => button.text() === 'imageArchive.resetView')!
    await reset.trigger('click')
    expect(viewer.text()).toContain('100%')
    for (let i = 0; i < 12; i++) await viewer.get('[aria-label="imageArchive.zoomOut"]').trigger('click')
    expect(viewer.text()).toContain('25%')
    expect(viewer.get('[aria-label="imageArchive.zoomOut"]').attributes('disabled')).toBeDefined()
    await wrapper.setProps({ file: { ...file, id: 2 } })
    expect(wrapper.find('[data-testid="viewer"]').exists()).toBe(false)
    expect(mocks.revoke).toHaveBeenCalledWith('blob:archive-test')
    wrapper.unmount()
  })

  it('pinches around two fingers and continues dragging smoothly with the remaining finger', async () => {
    mocks.file.mockResolvedValue(new Blob(['image']))
    const wrapper = mount(ImageArchiveAsset, { props: { recordID: 'record', file, full: true } })
    await flushPromises()
    await wrapper.get('img').trigger('click')
    expect(wrapper.get('[data-testid="viewer"]').classes()).toContain('image-archive-lightbox')
    const viewport = wrapper.get('[data-testid="archive-viewport"]')
    const release = vi.fn()
    Object.assign(viewport.element, {
      setPointerCapture: vi.fn(), hasPointerCapture: () => true, releasePointerCapture: release,
      getBoundingClientRect: () => ({ left: 0, top: 0, width: 400, height: 400 }),
    })
    const pointer = (id: number, x: number, y = 200) => ({ pointerId: id, pointerType: 'touch', clientX: x, clientY: y, isPrimary: id === 1 })
    await viewport.trigger('pointerdown', pointer(1, 150))
    await viewport.trigger('pointerdown', pointer(2, 250))
    await viewport.trigger('pointermove', pointer(1, 100))
    await viewport.trigger('pointermove', pointer(2, 300))
    expect(wrapper.text()).toContain('200%')
    expect(viewport.get('img').attributes('style')).toContain('translate(0px, 0px) scale(2)')
    await viewport.trigger('pointerup', pointer(2, 300))
    await viewport.trigger('pointermove', pointer(1, 120, 215))
    expect(viewport.get('img').attributes('style')).toContain('translate(20px, 15px) scale(2)')
    await viewport.trigger('pointercancel', pointer(1, 120, 215))
    await viewport.trigger('pointermove', pointer(1, 300, 300))
    expect(viewport.get('img').attributes('style')).toContain('translate(20px, 15px) scale(2)')
    expect(release).toHaveBeenCalledWith(1)
    await wrapper.get('[data-testid="close-viewer"]').trigger('click')
    expect(wrapper.find('[data-testid="viewer"]').exists()).toBe(false)
    await wrapper.get('img').trigger('click')
    expect(wrapper.text()).toContain('100%')
    wrapper.unmount()
  })

  it('limits touch zoom to 25–800% and ignores a third pointer', async () => {
    mocks.file.mockResolvedValue(new Blob(['image']))
    const wrapper = mount(ImageArchiveAsset, { props: { recordID: 'record', file, full: true } })
    await flushPromises()
    await wrapper.get('img').trigger('click')
    const viewport = wrapper.get('[data-testid="archive-viewport"]')
    Object.assign(viewport.element, { setPointerCapture: vi.fn(), hasPointerCapture: () => false })
    const pointer = (id: number, x: number) => ({ pointerId: id, pointerType: 'touch', clientX: x, clientY: 200 })
    await viewport.trigger('pointerdown', pointer(1, 150))
    await viewport.trigger('pointerdown', pointer(2, 250))
    await viewport.trigger('pointerdown', pointer(3, 500))
    await viewport.trigger('pointermove', pointer(3, 10000))
    expect(wrapper.text()).toContain('100%')
    await viewport.trigger('pointermove', pointer(2, 151))
    expect(wrapper.text()).toContain('25%')
    await viewport.trigger('pointermove', pointer(2, 10000))
    expect(wrapper.text()).toContain('800%')
    wrapper.unmount()
  })

  it('closes on an empty-space tap only when enabled, including object-contain letterboxing', async () => {
    mocks.file.mockResolvedValue(new Blob(['image']))
    const wrapper = mount(ImageArchiveAsset, { props: { recordID: 'record', file, full: true, closeOnBackdrop: false } })
    await flushPromises()
    await wrapper.get('img').trigger('click')
    const viewport = wrapper.get('[data-testid="archive-viewport"]')
    Object.assign(viewport.element, { setPointerCapture: vi.fn(), hasPointerCapture: () => false })
    const img = viewport.get('img').element
    Object.defineProperties(img, { naturalWidth: { value: 400 }, naturalHeight: { value: 200 } })
    img.getBoundingClientRect = () => ({ left: 0, top: 0, width: 400, height: 400 }) as DOMRect
    const tap = async (x: number, y: number) => {
      const point = { pointerId: 1, pointerType: 'mouse', button: 0, clientX: x, clientY: y }
      await viewport.trigger('pointerdown', point)
      await viewport.trigger('pointerup', point)
      await viewport.trigger('click', point)
    }
    await tap(200, 50)
    expect(wrapper.find('[data-testid="viewer"]').exists()).toBe(true)
    await wrapper.setProps({ closeOnBackdrop: true })
    await tap(200, 200) // Actual image: never close.
    expect(wrapper.find('[data-testid="viewer"]').exists()).toBe(true)
    await tap(200, 50) // Letterbox inside the image element's CSS box.
    expect(wrapper.find('[data-testid="viewer"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps the viewer open after a drag, a pinch, or a cancelled pointer on the backdrop', async () => {
    mocks.file.mockResolvedValue(new Blob(['image']))
    const wrapper = mount(ImageArchiveAsset, { props: { recordID: 'record', file, full: true, closeOnBackdrop: true } })
    await flushPromises()
    await wrapper.get('img').trigger('click')
    const viewport = wrapper.get('[data-testid="archive-viewport"]')
    Object.assign(viewport.element, { setPointerCapture: vi.fn(), hasPointerCapture: () => false })
    const img = viewport.get('img').element
    Object.defineProperties(img, { naturalWidth: { value: 100 }, naturalHeight: { value: 100 } })
    img.getBoundingClientRect = () => ({ left: 100, top: 100, width: 200, height: 200 }) as DOMRect
    const point = (id: number, x: number, y = 20) => ({ pointerId: id, pointerType: 'touch', clientX: x, clientY: y })
    await viewport.trigger('pointerdown', point(1, 20))
    await viewport.trigger('pointermove', point(1, 40))
    await viewport.trigger('pointerup', point(1, 40))
    await viewport.trigger('click', { clientX: 40, clientY: 20 })
    expect(wrapper.find('[data-testid="viewer"]').exists()).toBe(true)
    await viewport.trigger('pointerdown', point(1, 20))
    await viewport.trigger('pointerdown', point(2, 80))
    await viewport.trigger('pointerup', point(2, 80))
    await viewport.trigger('pointerup', point(1, 20))
    await viewport.trigger('click', { clientX: 20, clientY: 20 })
    expect(wrapper.find('[data-testid="viewer"]').exists()).toBe(true)
    await viewport.trigger('pointerdown', point(1, 20))
    await viewport.trigger('pointercancel', point(1, 20))
    await viewport.trigger('click', { clientX: 20, clientY: 20 })
    expect(wrapper.find('[data-testid="viewer"]').exists()).toBe(true)
    wrapper.unmount()
  })

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
