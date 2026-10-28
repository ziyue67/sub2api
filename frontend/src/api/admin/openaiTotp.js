import { apiClient } from '../client';
const base = (id) => `/admin/accounts/${id}`;
export async function getTOTPRotation(id) {
    return (await apiClient.get(`${base(id)}/totp-rotation`)).data;
}
export async function rotateTOTP(id) {
    return (await apiClient.post(`${base(id)}/totp-rotation`)).data;
}
export async function verifyTOTP(id, taskId, action) {
    await apiClient.post(`${base(id)}/totp-rotation/verify`, { task_id: taskId, action });
}
export async function exportTOTP(id, source, includePassword, signal) {
    return (await apiClient.post(`${base(id)}/totp-export`, { source, include_password: includePassword }, { signal })).data;
}
export async function exportChatGPTSession(id) {
    return (await apiClient.post(`${base(id)}/export-session`, {})).data;
}
export async function getChatGPTSessionExportTask(id, taskId) {
    return (await apiClient.get(`${base(id)}/export-session/${taskId}`)).data;
}
export async function downloadChatGPTSession(id, taskId) {
    return (await apiClient.get(`${base(id)}/export-session/${taskId}/download`)).data;
}
