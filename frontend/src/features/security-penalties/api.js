import { apiClient } from '@/api/client';
export async function getSummary() {
    return (await apiClient.get('/user/security/summary')).data;
}
export async function listEvents(page = 1, admin = false, userId) {
    return (await apiClient.get(admin ? '/admin/security-penalties/events' : '/user/security/events', { params: { page, page_size: 10, ...(admin && userId ? { user_id: userId } : {}) } })).data;
}
export async function getEvent(id, admin = false) {
    return (await apiClient.get(`${admin ? '/admin/security-penalties/events' : '/user/security/events'}/${id}`)).data;
}
export async function getConfig() {
    return (await apiClient.get('/admin/security-penalties/config')).data;
}
export async function saveConfig(config) {
    return (await apiClient.put('/admin/security-penalties/config', config)).data;
}
export async function release(userId, reason) {
    await apiClient.post(`/admin/security-penalties/users/${userId}/release`, { reason });
}
