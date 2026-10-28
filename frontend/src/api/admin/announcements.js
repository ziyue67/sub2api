/**
 * Admin Announcements API endpoints
 */
import { apiClient } from '../client';
export async function list(page = 1, pageSize = 20, filters, options) {
    const { data } = await apiClient.get('/admin/announcements', {
        params: { page, page_size: pageSize, ...filters },
        signal: options?.signal
    });
    return data;
}
export async function getById(id) {
    const { data } = await apiClient.get(`/admin/announcements/${id}`);
    return data;
}
export async function create(request) {
    const { data } = await apiClient.post('/admin/announcements', request);
    return data;
}
export async function update(id, request) {
    const { data } = await apiClient.put(`/admin/announcements/${id}`, request);
    return data;
}
export async function deleteAnnouncement(id) {
    const { data } = await apiClient.delete(`/admin/announcements/${id}`);
    return data;
}
export async function getReadStatus(id, page = 1, pageSize = 20, filters, options) {
    const { data } = await apiClient.get(`/admin/announcements/${id}/read-status`, {
        params: { page, page_size: pageSize, ...filters },
        signal: options?.signal
    });
    return data;
}
const announcementsAPI = {
    list,
    getById,
    create,
    update,
    delete: deleteAnnouncement,
    getReadStatus
};
export default announcementsAPI;
