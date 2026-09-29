import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, reactive, ref, toRefs } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { getAutoConfig, type AutoConfig } from '@/api/admin/autoConfig'
import { defaultExcelBPSDefaults, initialExcelBPSDefaults, type ExcelBPSMode } from '@/utils/excelBPSDefaults'
import { useExcelBPSDefaults } from '../useExcelBPSDefaults'

vi.mock('@/api/admin/autoConfig', () => ({ getAutoConfig: vi.fn() }))
const response = (): AutoConfig => ({ excel_bps: defaultExcelBPSDefaults() } as AutoConfig)
beforeEach(() => { vi.resetAllMocks(); vi.mocked(getAutoConfig).mockResolvedValue(response()) })

function harness(available = true) {
  const enabled = ref(false), mode = ref<ExcelBPSMode>('initial')
  const draft = reactive({ ...initialExcelBPSDefaults(), target_group_id: '' as number | string, models: ['existing-model'] })
  let controls!: ReturnType<typeof useExcelBPSDefaults>
  const component = defineComponent({
    props: { context: { type: String, default: 'account-1' } },
    setup(props) {
      controls = useExcelBPSDefaults({ enabled, mode, fields: toRefs(draft), context: () => props.context, available: () => available })
      return () => null
    }
  })
  return { wrapper: mount(component), enabled, mode, draft, get controls() { return controls } }
}

describe('BPS account activation modes', () => {
  it('leaves existing options untouched until the user operates a switch', () => {
    const h = harness()
    expect(getAutoConfig).not.toHaveBeenCalled()
    expect(h.enabled.value).toBe(false)
    expect(h.draft.models).toEqual(['existing-model'])
    h.wrapper.unmount()
  })

  it('fills saved defaults when the defaults switch is enabled', async () => {
    const h = harness()
    await h.controls.toggle('defaults')
    expect(h.enabled.value).toBe(true)
    expect(h.mode.value).toBe('defaults')
    expect(h.draft).toEqual({ ...defaultExcelBPSDefaults(), target_group_id: '' })
    expect(h.controls.applied.value).toBe(true)
    h.wrapper.unmount()
  })

  it('fills initial settings without fetching the saved template', async () => {
    const h = harness()
    await h.controls.toggle('initial')
    expect(h.enabled.value).toBe(true)
    expect(h.mode.value).toBe('initial')
    expect(h.draft).toEqual({ ...initialExcelBPSDefaults(), target_group_id: '' })
    expect(getAutoConfig).not.toHaveBeenCalled()
    h.wrapper.unmount()
  })

  it('switches between mutually exclusive modes and turns off the active mode', async () => {
    const h = harness()
    await h.controls.toggle('defaults')
    h.draft.models = ['custom-model']
    await h.controls.toggle('initial')
    expect(h.enabled.value).toBe(true)
    expect(h.mode.value).toBe('initial')
    expect(h.draft).toEqual({ ...initialExcelBPSDefaults(), target_group_id: '' })
    await h.controls.toggle('initial')
    expect(h.enabled.value).toBe(false)
    await h.controls.toggle('defaults')
    expect(h.enabled.value).toBe(true)
    expect(h.mode.value).toBe('defaults')
    expect(h.draft).toEqual({ ...defaultExcelBPSDefaults(), target_group_id: '' })
    expect(getAutoConfig).toHaveBeenCalledTimes(2)
    h.wrapper.unmount()
  })

  it('keeps initial configuration available to observers', async () => {
    const h = harness(false)
    await h.controls.toggle('defaults')
    expect(h.enabled.value).toBe(false)
    expect(getAutoConfig).not.toHaveBeenCalled()
    await h.controls.toggle('initial')
    expect(h.enabled.value).toBe(true)
    expect(h.draft).toEqual({ ...initialExcelBPSDefaults(), target_group_id: '' })
    h.wrapper.unmount()
  })

  it('discards a pending template after switching or closing an account', async () => {
    let resolve!: (value: AutoConfig) => void
    vi.mocked(getAutoConfig).mockReturnValue(new Promise(r => { resolve = r }))
    const h = harness()
    const pending = h.controls.toggle('defaults')
    expect(h.controls.loading.value).toBe(true)
    await h.wrapper.setProps({ context: 'account-2' })
    resolve(response()); await pending; await flushPromises()
    expect(h.enabled.value).toBe(false)
    expect(h.draft.models).toEqual(['existing-model'])
    expect(h.controls.applied.value).toBe(false)
    h.wrapper.unmount()
  })

  it('keeps the active mode and edits on template failure, then supports retry', async () => {
    vi.mocked(getAutoConfig).mockRejectedValueOnce(new Error('offline'))
    const h = harness()
    await h.controls.toggle('initial')
    h.draft.models = ['my-model']
    await h.controls.toggle('defaults')
    expect(h.enabled.value).toBe(true)
    expect(h.mode.value).toBe('initial')
    expect(h.draft.models).toEqual(['my-model'])
    expect(h.controls.failed.value).toBe(true)
    await h.controls.toggle('defaults')
    expect(h.mode.value).toBe('defaults')
    expect(h.controls.failed.value).toBe(false)
    h.wrapper.unmount()
  })
})
