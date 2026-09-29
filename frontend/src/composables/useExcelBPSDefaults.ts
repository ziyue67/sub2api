import { onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { getAutoConfig } from '@/api/admin/autoConfig'
import { defaultExcelBPSDefaults, initialExcelBPSDefaults, type ExcelBPSDefaults, type ExcelBPSMode } from '@/utils/excelBPSDefaults'

type BPSFormRefs = { [K in keyof ExcelBPSDefaults]: Ref<ExcelBPSDefaults[K]> }
type FormRefs = Omit<BPSFormRefs, 'recovery_interval_minutes' | 'target_group_id'> & {
  recovery_interval_minutes: Ref<number | string>
  target_group_id: Ref<number | string>
}

// Both activation modes are explicit edits to the current account form only.
export function useExcelBPSDefaults(options: {
  enabled: Ref<boolean>
  mode: Ref<ExcelBPSMode>
  fields: FormRefs
  context: () => string
  available?: () => boolean
}) {
  const loading = ref(false), failed = ref(false), applied = ref(false)
  let generation = 0
  watch(options.context, () => {
    generation++
    loading.value = failed.value = applied.value = false
  }, { flush: 'sync' })
  onBeforeUnmount(() => { generation++ })

  function fill(defaults: ExcelBPSDefaults, mode: ExcelBPSMode) {
    for (const key of Object.keys(options.fields) as (keyof FormRefs)[]) {
      const target = options.fields[key] as Ref<unknown>
      target.value = key === 'models' ? [...defaults.models] : key === 'target_group_id' && !defaults.auto_move_on_403 ? '' : defaults[key]
    }
    options.mode.value = mode
    options.enabled.value = true
    applied.value = true
  }

  async function toggle(mode: ExcelBPSMode) {
    if (loading.value) return
    failed.value = false
    if (options.enabled.value && options.mode.value === mode) {
      options.enabled.value = false
      applied.value = false
      return
    }
    if (mode === 'defaults' && options.available?.() === false) return
    if (mode === 'initial') {
      fill(initialExcelBPSDefaults(), mode)
      return
    }
    const current = generation
    loading.value = true
    try {
      const config = await getAutoConfig()
      if (generation === current) fill(config.excel_bps ?? defaultExcelBPSDefaults(), mode)
    } catch {
      if (generation === current) failed.value = true
    } finally {
      if (generation === current) loading.value = false
    }
  }
  return { loading, failed, applied, toggle }
}
