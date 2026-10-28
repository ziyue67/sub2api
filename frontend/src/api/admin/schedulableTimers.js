import { apiClient } from '../client';
export const schedulableTimersAPI = {
    async list(id) {
        return (await apiClient.get(`/admin/accounts/${id}/schedulable-timers`)).data;
    },
    async preview(id, input) {
        return (await apiClient.post(`/admin/accounts/${id}/schedulable-timers/preview`, input)).data;
    },
    async save(id, input) {
        return (await apiClient.post(`/admin/accounts/${id}/schedulable-timers`, input)).data;
    },
    async cancel(id, action, version) {
        await apiClient.delete(`/admin/accounts/${id}/schedulable-timers/${action}`, { params: { version } });
    }
};
