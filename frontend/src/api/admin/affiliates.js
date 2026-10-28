/**
 * Admin Affiliate API endpoints
 * Manage per-user affiliate (邀请返利) configurations:
 * exclusive invite codes (overrides aff_code) and exclusive rebate rates.
 */
import { apiClient } from '../client';
export async function listUsers(params = {}) {
    const { data } = await apiClient.get('/admin/affiliates/users', {
        params: {
            page: params.page ?? 1,
            page_size: params.page_size ?? 20,
            search: params.search ?? '',
        },
    });
    return data;
}
export async function lookupUsers(q) {
    const { data } = await apiClient.get('/admin/affiliates/users/lookup', { params: { q } });
    return data;
}
export async function updateUserSettings(userId, payload) {
    const { data } = await apiClient.put(`/admin/affiliates/users/${userId}`, payload);
    return data;
}
export async function clearUserSettings(userId) {
    const { data } = await apiClient.delete(`/admin/affiliates/users/${userId}`);
    return data;
}
export async function batchSetRate(payload) {
    const { data } = await apiClient.post('/admin/affiliates/users/batch-rate', payload);
    return data;
}
function recordParams(params = {}) {
    return {
        page: params.page ?? 1,
        page_size: params.page_size ?? 20,
        search: params.search ?? '',
        start_at: params.start_at || undefined,
        end_at: params.end_at || undefined,
        sort_by: params.sort_by || undefined,
        sort_order: params.sort_order || undefined,
        timezone: params.timezone || undefined,
    };
}
export async function listInviteRecords(params = {}) {
    const { data } = await apiClient.get('/admin/affiliates/invites', { params: recordParams(params) });
    return data;
}
export async function listRebateRecords(params = {}) {
    const { data } = await apiClient.get('/admin/affiliates/rebates', { params: recordParams(params) });
    return data;
}
export async function listTransferRecords(params = {}) {
    const { data } = await apiClient.get('/admin/affiliates/transfers', { params: recordParams(params) });
    return data;
}
export async function getUserOverview(userId) {
    const { data } = await apiClient.get(`/admin/affiliates/users/${userId}/overview`);
    return data;
}
/**
 * Records an offline withdrawal. `idempotencyKey` identifies one registration:
 * a retry with the same key deducts nothing and returns the first result, with
 * `replayed` set.
 */
export async function withdrawUserQuota(userId, payload, idempotencyKey) {
    const response = await apiClient.post(`/admin/affiliates/users/${userId}/withdraw`, payload, { headers: { 'Idempotency-Key': idempotencyKey } });
    return {
        result: response.data,
        replayed: response.headers?.['x-idempotency-replayed'] === 'true',
    };
}
export const affiliatesAPI = {
    listUsers,
    lookupUsers,
    updateUserSettings,
    clearUserSettings,
    batchSetRate,
    listInviteRecords,
    listRebateRecords,
    listTransferRecords,
    getUserOverview,
    withdrawUserQuota,
};
export default affiliatesAPI;
