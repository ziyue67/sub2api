import { apiClient } from '@/api/client';
export const adminComplianceAPI = {
    async getStatus() {
        const { data } = await apiClient.get('/admin/compliance');
        return data;
    },
    async accept(payload) {
        const { data } = await apiClient.post('/admin/compliance/accept', payload);
        return data;
    }
};
export default adminComplianceAPI;
