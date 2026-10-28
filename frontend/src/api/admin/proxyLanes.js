/** Admin API for independently schedulable account egress lanes. */
import { apiClient } from '../client';
export async function list(accountId) {
    const { data } = await apiClient.get(`/admin/accounts/${accountId}/proxy-lanes`);
    return data ?? [];
}
export async function create(accountId, payload) {
    const { data } = await apiClient.post(`/admin/accounts/${accountId}/proxy-lanes`, payload);
    return data;
}
export async function update(accountId, laneId, payload) {
    const { data } = await apiClient.put(`/admin/accounts/${accountId}/proxy-lanes/${laneId}`, payload);
    return data;
}
export async function remove(accountId, laneId) {
    await apiClient.delete(`/admin/accounts/${accountId}/proxy-lanes/${laneId}`);
}
export const proxyLanesAPI = { list, create, update, remove };
export default proxyLanesAPI;
