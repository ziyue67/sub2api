import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import QualityModelShortcuts from '../QualityModelShortcuts.vue'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

afterEach(() => vi.unstubAllGlobals())

describe('QualityModelShortcuts', () => {
  it('keeps shortcuts usable and reports when browser persistence fails', async () => {
    const storage = {
      getItem: vi.fn(() => null),
      setItem: vi.fn(() => { throw new Error('Storage blocked') }),
    }
    vi.stubGlobal('localStorage', storage)

    const wrapper = mount(QualityModelShortcuts, {
      props: { selectedModels: [], userId: 1 },
    })
    await wrapper.get('summary').trigger('click')
    await wrapper.get('[data-testid="quality-model-shortcuts-input"]').setValue('custom-model')
    await wrapper.get('[data-testid="quality-save-model-shortcuts"]').trigger('click')
    await flushPromises()

    expect(storage.setItem).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-testid="quality-model-shortcuts-error"]').text()).toBe('qualityOps.modelShortcutsSaveFailed')
    expect(wrapper.findAll('[data-testid="quality-model-shortcut"]').map(button => button.text())).toContain('custom-model')
    wrapper.unmount()
  })
})
