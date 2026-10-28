import { apiClient } from '../client';
export async function readProfitOperations(params, signal) {
    return (await apiClient.get('/admin/profit-operations/snapshot', { params, signal })).data;
}
