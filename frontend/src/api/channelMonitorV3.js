import { apiClient } from './client';
export const MONITOR_V3_INTERVALS = [1, 2, 3, 5, 10, 15, 30, 60];
/** V2 latency histogram bounds; thresholds compare exactly against the P50 bucket. */
export const MONITOR_V3_TTFT_THRESHOLDS = [1000, 2000, 3000, 5000, 8000, 10000, 15000, 30000, 60000, 120000];
export const MONITOR_V3_RANGES = ['24h', '7d', '30d'];
/** Reasoning efforts offered for idle probes; '' keeps the model default. */
export const MONITOR_V3_PROBE_EFFORTS = ['', 'low', 'medium', 'high'];
const base = (admin) => (admin ? '/admin/channel-monitor-v3' : '/channel-monitor-v3');
/** `end` (unix seconds) selects an older window by its last slot. */
export async function getStatus(end, admin = false, signal) {
    const { data } = await apiClient.get(`${base(admin)}/status`, {
        params: end ? { end } : undefined,
        signal,
    });
    return data;
}
export async function getIncidents(page, pageSize, admin = false, signal) {
    const { data } = await apiClient.get(`${base(admin)}/incidents`, {
        params: { page, page_size: pageSize },
        signal,
    });
    return data;
}
export async function getSettings() {
    const { data } = await apiClient.get('/admin/channel-monitor-v3/settings');
    return data;
}
export async function updateConfig(config) {
    const { data } = await apiClient.put('/admin/channel-monitor-v3/config', config);
    return data;
}
/** Saves which groups get idle probes; the page config is left alone. */
export async function updateIdleProbe(probe) {
    const { data } = await apiClient.put('/admin/channel-monitor-v3/idle-probe', probe);
    return data;
}
export async function createCategory(input) {
    const { data } = await apiClient.post('/admin/channel-monitor-v3/categories', input);
    return data;
}
export async function updateCategory(id, input) {
    const { data } = await apiClient.put(`/admin/channel-monitor-v3/categories/${id}`, input);
    return data;
}
export async function deleteCategory(id) {
    await apiClient.delete(`/admin/channel-monitor-v3/categories/${id}`);
}
export async function createComponent(input) {
    const { data } = await apiClient.post('/admin/channel-monitor-v3/components', input);
    return data;
}
export async function updateComponent(id, input) {
    const { data } = await apiClient.put(`/admin/channel-monitor-v3/components/${id}`, input);
    return data;
}
export async function deleteComponent(id) {
    await apiClient.delete(`/admin/channel-monitor-v3/components/${id}`);
}
export async function reorder(categoryIds, componentIds) {
    await apiClient.post('/admin/channel-monitor-v3/reorder', { category_ids: categoryIds, component_ids: componentIds });
}
/** Switches the site between V1, V2 and V3 without a full settings save. */
export async function setMonitorMode(mode) {
    const { data } = await apiClient.put('/admin/channel-monitor-mode', { mode });
    return data.mode;
}
