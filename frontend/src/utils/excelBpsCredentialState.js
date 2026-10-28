import { createSharedComposable, useTimestamp } from '@vueuse/core';
export const useExcelCredentialClock = createSharedComposable(() => useTimestamp({ interval: 30000 }));
export function excelCredentialStatus(state, now) {
    const expiry = state.expires_at ? Date.parse(state.expires_at) : NaN;
    if (state.status !== 'revoked' && Number.isFinite(expiry) && expiry <= now)
        return 'expired';
    return ['unknown', 'pending', 'not_expired', 'expired', 'revoked', 'auth_failed'].includes(state.status) ? state.status : 'unknown';
}
