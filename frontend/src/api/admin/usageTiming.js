import { apiClient } from '../client';
export async function getUsageTiming(id, signal) {
    const { data } = await apiClient.get(`/admin/usage/${id}/timing`, { signal });
    return data;
}
