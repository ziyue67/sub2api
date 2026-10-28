import { onBeforeUnmount, ref, watch } from 'vue';
import { getAutoConfig } from '@/api/admin/autoConfig';
import { defaultExcelBPSDefaults, initialExcelBPSDefaults } from '@/utils/excelBPSDefaults';
// Both activation modes are explicit edits to the current account form only.
export function useExcelBPSDefaults(options) {
    const loading = ref(false), failed = ref(false), applied = ref(false);
    let generation = 0;
    watch(options.context, () => {
        generation++;
        loading.value = failed.value = applied.value = false;
    }, { flush: 'sync' });
    onBeforeUnmount(() => { generation++; });
    function fill(defaults, mode) {
        for (const key of Object.keys(options.fields)) {
            const target = options.fields[key];
            target.value = key === 'models' ? [...defaults.models] : key === 'target_group_id' && !defaults.auto_move_on_403 ? '' : defaults[key];
        }
        options.mode.value = mode;
        options.enabled.value = true;
        applied.value = true;
    }
    async function toggle(mode) {
        if (loading.value)
            return;
        failed.value = false;
        if (options.enabled.value && options.mode.value === mode) {
            options.enabled.value = false;
            applied.value = false;
            return;
        }
        if (mode === 'defaults' && options.available?.() === false)
            return;
        if (mode === 'initial') {
            fill(initialExcelBPSDefaults(), mode);
            return;
        }
        const current = generation;
        loading.value = true;
        try {
            const config = await getAutoConfig();
            if (generation === current)
                fill(config.excel_bps ?? defaultExcelBPSDefaults(), mode);
        }
        catch {
            if (generation === current)
                failed.value = true;
        }
        finally {
            if (generation === current)
                loading.value = false;
        }
    }
    return { loading, failed, applied, toggle };
}
