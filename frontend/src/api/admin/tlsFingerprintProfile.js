/**
 * Admin TLS Fingerprint Profile API endpoints
 * Handles TLS fingerprint profile CRUD for administrators
 */
import { apiClient } from '../client';
export async function list() {
    const { data } = await apiClient.get('/admin/tls-fingerprint-profiles');
    return data;
}
export async function getById(id) {
    const { data } = await apiClient.get(`/admin/tls-fingerprint-profiles/${id}`);
    return data;
}
export async function create(profileData) {
    const { data } = await apiClient.post('/admin/tls-fingerprint-profiles', profileData);
    return data;
}
export async function update(id, updates) {
    const { data } = await apiClient.put(`/admin/tls-fingerprint-profiles/${id}`, updates);
    return data;
}
export async function deleteProfile(id) {
    const { data } = await apiClient.delete(`/admin/tls-fingerprint-profiles/${id}`);
    return data;
}
export const tlsFingerprintProfileAPI = {
    list,
    getById,
    create,
    update,
    delete: deleteProfile
};
export default tlsFingerprintProfileAPI;
