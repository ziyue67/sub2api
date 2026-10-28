import { apiClient } from './client';
/** Ordered taxonomy mirrored from backend ChannelMonitorV2ErrorCategories. */
export const MONITOR_ERROR_CATEGORIES = [
    'content_policy',
    'authentication',
    'context_limit',
    'invalid_request',
    'model_unsupported',
    'group_access',
    'quota_or_balance',
    'account_pool_unavailable',
    'rate_or_capacity',
    'timeout',
    'transport_or_stream',
    'upstream_forbidden',
    'not_found',
    'client_cancelled',
    'upstream_5xx',
    'internal',
    'other',
];
function params(filter) {
    return {
        range: filter.range,
        platform: filter.platforms.length ? filter.platforms : undefined,
        group_id: filter.groupIds.length ? filter.groupIds : undefined,
        model: filter.models.length ? filter.models : undefined,
    };
}
export function repeatedArrayParamsSerializer(values) {
    const search = new URLSearchParams();
    for (const [key, rawValue] of Object.entries(values)) {
        if (rawValue == null || rawValue === '')
            continue;
        const entries = Array.isArray(rawValue) ? rawValue : [rawValue];
        for (const value of entries) {
            if (value != null && value !== '')
                search.append(key, String(value));
        }
    }
    return search.toString();
}
const requestConfig = (filter, signal, extraParams = {}) => ({
    params: { ...params(filter), ...extraParams },
    paramsSerializer: { serialize: repeatedArrayParamsSerializer },
    signal,
});
function base(admin) { return admin ? '/admin/channel-monitor-v2' : '/channel-monitor-v2'; }
export async function getDimensions(filter, admin = false, signal) {
    const { data } = await apiClient.get(`${base(admin)}/dimensions`, requestConfig(filter, signal));
    return data;
}
export async function getSnapshot(filter, admin = false, signal) {
    const { data } = await apiClient.get(`${base(admin)}/snapshot`, requestConfig(filter, signal));
    return data;
}
export async function getMatrix(filter, groupBy, admin = false, signal, options) {
    const { data } = await apiClient.get(`${base(admin)}/matrix`, requestConfig(filter, signal, {
        group_by: groupBy,
        recent_samples: options?.recentSamples ? 1 : undefined,
    }));
    return data;
}
export async function getModels(filter, admin = false, signal) {
    const { data } = await apiClient.get(`${base(admin)}/models`, requestConfig(filter, signal));
    return data;
}
export async function getErrors(filter, admin = false, signal) {
    const { data } = await apiClient.get(`${base(admin)}/errors`, requestConfig(filter, signal));
    return data;
}
export async function getUsers(filter, admin = false, signal) {
    const { data } = await apiClient.get(`${base(admin)}/users`, requestConfig(filter, signal));
    return data;
}
export async function getConfig() {
    const { data } = await apiClient.get('/admin/channel-monitor-v2/config');
    return data;
}
export async function updateConfig(config) {
    const { data } = await apiClient.put('/admin/channel-monitor-v2/config', config);
    return data;
}
