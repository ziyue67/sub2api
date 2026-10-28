import { apiClient } from '../client';
const base = '/admin/risk-control';
export async function getEvidence(id) {
    return (await apiClient.get(`${base}/logs/${id}/evidence`)).data;
}
export async function exportEvidence(id) {
    return (await apiClient.get(`${base}/logs/${id}/evidence/export`, { responseType: 'blob' })).data;
}
export async function deleteEvidence(id) {
    await apiClient.delete(`${base}/logs/${id}/evidence`);
}
export async function getEvidenceConfig() {
    return (await apiClient.get(`${base}/evidence/config`)).data;
}
export async function updateEvidenceConfig(config) {
    return (await apiClient.put(`${base}/evidence/config`, config)).data;
}
