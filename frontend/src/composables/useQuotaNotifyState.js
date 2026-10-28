import { reactive, ref } from 'vue';
import { adminAPI } from '@/api/admin';
import { QUOTA_THRESHOLD_TYPE_FIXED } from '@/constants/account';
export const QUOTA_NOTIFY_DIMS = ['daily', 'weekly', 'total'];
export function useQuotaNotifyState() {
    const globalEnabled = ref(false);
    const state = reactive({
        daily: { enabled: null, threshold: null, thresholdType: null },
        weekly: { enabled: null, threshold: null, thresholdType: null },
        total: { enabled: null, threshold: null, thresholdType: null },
    });
    function loadGlobalState() {
        adminAPI.accounts
            .getManagementCapabilities()
            .then((settings) => {
            globalEnabled.value = settings.account_quota_notify_enabled === true;
        })
            .catch(() => {
            globalEnabled.value = false;
        });
    }
    function loadFromExtra(extra) {
        for (const d of QUOTA_NOTIFY_DIMS) {
            state[d].enabled = extra?.[`quota_notify_${d}_enabled`] ?? null;
            state[d].threshold = extra?.[`quota_notify_${d}_threshold`] ?? null;
            state[d].thresholdType = extra?.[`quota_notify_${d}_threshold_type`] ?? null;
        }
    }
    function writeToExtra(extra, mode) {
        for (const d of QUOTA_NOTIFY_DIMS) {
            const s = state[d];
            if (s.enabled) {
                extra[`quota_notify_${d}_enabled`] = true;
                if (s.threshold != null) {
                    extra[`quota_notify_${d}_threshold`] = s.threshold;
                }
                else if (mode === 'update') {
                    delete extra[`quota_notify_${d}_threshold`];
                }
                extra[`quota_notify_${d}_threshold_type`] = s.thresholdType || QUOTA_THRESHOLD_TYPE_FIXED;
            }
            else if (mode === 'update') {
                delete extra[`quota_notify_${d}_enabled`];
                delete extra[`quota_notify_${d}_threshold`];
                delete extra[`quota_notify_${d}_threshold_type`];
            }
        }
    }
    function reset() {
        for (const d of QUOTA_NOTIFY_DIMS) {
            state[d].enabled = null;
            state[d].threshold = null;
            state[d].thresholdType = null;
        }
    }
    return { globalEnabled, state, loadGlobalState, loadFromExtra, writeToExtra, reset };
}
