import { apiClient } from '../client';
const base = '/admin/accounts';
export async function getCodexHarvestControls() {
    const { data } = await apiClient.get(`${base}/codex-harvest-controls`);
    return data;
}
export async function saveCodexHarvestControls(settings) {
    const { data } = await apiClient.put(`${base}/codex-harvest-controls`, settings);
    return data;
}
export async function getCodexHarvestNodes(offset = 0, limit = 20) {
    const { data } = await apiClient.get(`${base}/codex-harvest-nodes`, { params: { offset, limit } });
    return { items: data.items || [], total: data.total || 0 };
}
export async function resetCodexHarvestNodes(recordId) {
    await apiClient.post(`${base}/codex-harvest-nodes/reset`, { record_id: recordId });
}
