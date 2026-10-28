import { apiClient } from './client';
export async function getTrafficKeys(admin, userId, query = '', signal) {
    const { data } = await apiClient.get(admin ? '/admin/usage/traffic/keys' : '/usage/traffic/keys', {
        params: { q: query, ...(admin && userId ? { user_id: userId } : {}) }, signal
    });
    return data;
}
export async function getTrafficHealth(signal) {
    const { data } = await apiClient.get('/admin/usage/traffic/health', { signal });
    return data;
}
export async function getTraffic(admin, params, signal) {
    const { data } = await apiClient.get(admin ? '/admin/usage/traffic' : '/usage/traffic', { params, signal });
    return data;
}
export function formatTrafficBytes(value) {
    if (value < 1024)
        return `${value} B`;
    const units = ['KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
    let scaled = value / 1024;
    let index = 0;
    while (scaled >= 1024 && index < units.length - 1) {
        scaled /= 1024;
        index++;
    }
    return `${scaled.toFixed(2)} ${units[index]}`;
}
