import { apiClient } from '../client';
export async function getConfig() {
    const { data } = await apiClient.get('/admin/risk-control/config');
    return data;
}
export async function updateConfig(payload) {
    const { data } = await apiClient.put('/admin/risk-control/config', payload);
    return data;
}
export async function getStatus() {
    const { data } = await apiClient.get('/admin/risk-control/status');
    return data;
}
export async function testAPIKeys(payload = {}) {
    const { data } = await apiClient.post('/admin/risk-control/api-keys/test', payload);
    return data;
}
export async function listLogs(params = {}) {
    const { data } = await apiClient.get('/admin/risk-control/logs', {
        params,
    });
    return data;
}
export async function getModerationUser(userID) {
    const { data } = await apiClient.get(`/admin/risk-control/users/${userID}`);
    return data;
}
export async function manageModerationUser(userID, payload) {
    const { data } = await apiClient.post(`/admin/risk-control/users/${userID}/actions`, payload);
    return data;
}
export async function listModerationUsers(params) {
    const { data } = await apiClient.get('/admin/risk-control/users', { params });
    return data;
}
export async function unbanUser(userID, payload) {
    const { data } = await apiClient.post(`/admin/risk-control/users/${userID}/unban`, payload);
    return data;
}
export async function deleteFlaggedHash(inputHash) {
    const { data } = await apiClient.delete('/admin/risk-control/hashes', {
        data: { input_hash: inputHash },
    });
    return data;
}
export async function clearFlaggedHashes() {
    const { data } = await apiClient.delete('/admin/risk-control/hashes/all');
    return data;
}
export const riskControlAPI = {
    getConfig,
    updateConfig,
    getStatus,
    testAPIKeys,
    listLogs,
    unbanUser,
    getModerationUser,
    manageModerationUser,
    listModerationUsers,
    deleteFlaggedHash,
    clearFlaggedHashes,
};
export default riskControlAPI;
