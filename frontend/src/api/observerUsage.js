import { apiClient } from './client';
// Identity is supplied by the authenticated backend, never by page filters.
function ownParams(params) {
    const { user_id: _userId, ...own } = params;
    return own;
}
export const observerUsageAPI = {
    async list(params, options) {
        const { data } = await apiClient.get('/usage', { params: ownParams(params), signal: options?.signal });
        return data;
    },
    async getStats(params) {
        const { data } = await apiClient.get('/usage/stats', { params: ownParams(params) });
        return data;
    },
    async getModelStats(params) {
        const { data } = await apiClient.get('/usage/dashboard/models', { params: ownParams(params) });
        return data;
    },
    async getSnapshotV2(params) {
        const { data } = await apiClient.get('/usage/dashboard/snapshot-v2', { params: ownParams(params) });
        return data;
    },
    async filterOptions(kind, q = '') {
        const { data } = await apiClient.get('/usage/filter-options', { params: { kind, q } });
        return data;
    },
    async getTiming(id, signal) {
        const { data } = await apiClient.get(`/usage/${id}/timing`, { signal });
        return data;
    },
    async listErrors(params) {
        const { user_id: _userId, ...own } = params;
        const { data } = await apiClient.get('/usage/errors', { params: own });
        return data;
    },
    async getErrorDetail(id) {
        const { data } = await apiClient.get(`/usage/errors/${id}`);
        return data;
    }
};
