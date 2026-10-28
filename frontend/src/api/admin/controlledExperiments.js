import { apiClient } from '../client';
export const controlledExperimentsAPI = {
    async catalog() {
        const { data } = await apiClient.get('/admin/controlled-experiments/catalog');
        return data;
    },
    async list(beforeId = 0) {
        const { data } = await apiClient.get('/admin/controlled-experiments', { params: { before_id: beforeId } });
        return data ?? [];
    },
    async create(input) {
        const { data } = await apiClient.post('/admin/controlled-experiments', input);
        return data;
    },
    async report(id) {
        const { data } = await apiClient.get(`/admin/controlled-experiments/${id}`);
        return data;
    },
    async start(id) { await apiClient.post(`/admin/controlled-experiments/${id}/start`); },
    async stop(id) { await apiClient.post(`/admin/controlled-experiments/${id}/stop`); }
};
