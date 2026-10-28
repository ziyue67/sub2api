import { apiClient } from '../client';
export async function listQualityPlans() {
    const { data } = await apiClient.get('/admin/account-quality-plans');
    return data ?? [];
}
export async function runQualityPlan(id) {
    await apiClient.post(`/admin/account-quality-plans/${id}/run`);
}
export async function listQualityOperations(beforeId = 0) {
    const { data } = await apiClient.get('/admin/account-quality-results', { params: { before_id: beforeId } });
    return { items: data.items ?? [], next_cursor: data.next_cursor ?? 0 };
}
export async function listQualityTemplates() {
    const { data } = await apiClient.get('/admin/account-quality-templates');
    return data ?? [];
}
export async function createQualityTemplate(body) {
    const { data } = await apiClient.post('/admin/account-quality-templates', body);
    return data;
}
export async function updateQualityTemplate(id, body) {
    const { data } = await apiClient.put(`/admin/account-quality-templates/${id}`, body);
    return data;
}
export async function deleteQualityTemplate(id, deletePlans) {
    const { data } = await apiClient.delete(`/admin/account-quality-templates/${id}`, { params: { delete_plans: deletePlans ? 'true' : undefined } });
    return data;
}
export async function listQualityPresets() {
    const { data } = await apiClient.get('/admin/account-quality-presets');
    return data ?? [];
}
export async function createQualityPreset(body) {
    const { data } = await apiClient.post('/admin/account-quality-presets', body);
    return data;
}
export async function updateQualityPreset(id, body) {
    const { data } = await apiClient.put(`/admin/account-quality-presets/${id}`, body);
    return data;
}
export async function previewQualityOwnership(id) {
    const { data } = await apiClient.get(`/admin/account-quality-plans/${id}/ownership`);
    return data;
}
export async function reconcileQualityOwnership(id, fence) {
    const { data } = await apiClient.post(`/admin/account-quality-plans/${id}/ownership/reconcile`, { fence, confirm: true });
    return data;
}
export async function restoreQualityOwnership(id, fence) {
    const { data } = await apiClient.post(`/admin/account-quality-plans/${id}/ownership/restore`, { fence, confirm: true });
    return data;
}
export async function removeQualityOwnedModel(id, fence, model, reason_code) {
    const { data } = await apiClient.post(`/admin/account-quality-plans/${id}/ownership/remove-model`, { fence, model, reason_code, confirm: true });
    return data;
}
