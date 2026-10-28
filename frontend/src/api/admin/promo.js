/**
 * Admin Promo Codes API endpoints
 */
import { apiClient } from '../client';
export async function list(page = 1, pageSize = 20, filters, options) {
    const { data } = await apiClient.get('/admin/promo-codes', {
        params: { page, page_size: pageSize, ...filters },
        signal: options?.signal
    });
    return data;
}
export async function getById(id) {
    const { data } = await apiClient.get(`/admin/promo-codes/${id}`);
    return data;
}
export async function create(request) {
    const { data } = await apiClient.post('/admin/promo-codes', request);
    return data;
}
export async function update(id, request) {
    const { data } = await apiClient.put(`/admin/promo-codes/${id}`, request);
    return data;
}
export async function deleteCode(id) {
    const { data } = await apiClient.delete(`/admin/promo-codes/${id}`);
    return data;
}
export async function getUsages(id, page = 1, pageSize = 20) {
    const { data } = await apiClient.get(`/admin/promo-codes/${id}/usages`, { params: { page, page_size: pageSize } });
    return data;
}
const promoAPI = {
    list,
    getById,
    create,
    update,
    delete: deleteCode,
    getUsages
};
export default promoAPI;
