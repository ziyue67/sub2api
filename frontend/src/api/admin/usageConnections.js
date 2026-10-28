import { apiClient } from '../client';
export async function listConnections(params, signal) {
    const { data } = await apiClient.get('/admin/usage/connections', { params, signal });
    return data;
}
