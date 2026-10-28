/**
 * Admin Email Broadcasts API endpoints.
 *
 * Bulk announcement emails composed and sent by administrators from the admin
 * panel. Backed by POST /api/v1/admin/email-broadcasts on the server.
 */
import { apiClient } from '../client';
async function list(page = 1, pageSize = 20, status) {
    const params = {
        page,
        page_size: pageSize
    };
    if (status)
        params.status = status;
    const { data } = await apiClient.get('/admin/email-broadcasts', { params });
    return data;
}
async function getById(id) {
    const { data } = await apiClient.get(`/admin/email-broadcasts/${id}`);
    return data;
}
async function create(request) {
    const { data } = await apiClient.post('/admin/email-broadcasts', request);
    return data;
}
async function searchRecipients(query, limit = 20) {
    const { data } = await apiClient.get('/admin/email-broadcasts/recipients/search', {
        params: { q: query, limit }
    });
    return data;
}
async function preview(request, options) {
    const { data } = await apiClient.post('/admin/email-broadcasts/preview', request, {
        signal: options?.signal
    });
    return data;
}
async function deleteBroadcast(id) {
    await apiClient.delete(`/admin/email-broadcasts/${id}`);
}
const emailBroadcastsAPI = {
    list,
    getById,
    create,
    searchRecipients,
    preview,
    delete: deleteBroadcast
};
export default emailBroadcastsAPI;
