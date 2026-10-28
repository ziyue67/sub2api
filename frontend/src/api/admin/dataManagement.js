import { apiClient } from '../client';
export async function getAgentHealth() {
    const { data } = await apiClient.get('/admin/data-management/agent/health');
    return data;
}
export async function getConfig() {
    const { data } = await apiClient.get('/admin/data-management/config');
    return data;
}
export async function updateConfig(request) {
    const { data } = await apiClient.put('/admin/data-management/config', request);
    return data;
}
export async function testS3(request) {
    const { data } = await apiClient.post('/admin/data-management/s3/test', request);
    return data;
}
export async function listSourceProfiles(sourceType) {
    const { data } = await apiClient.get(`/admin/data-management/sources/${sourceType}/profiles`);
    return data;
}
export async function createSourceProfile(sourceType, request) {
    const { data } = await apiClient.post(`/admin/data-management/sources/${sourceType}/profiles`, request);
    return data;
}
export async function updateSourceProfile(sourceType, profileID, request) {
    const { data } = await apiClient.put(`/admin/data-management/sources/${sourceType}/profiles/${profileID}`, request);
    return data;
}
export async function deleteSourceProfile(sourceType, profileID) {
    await apiClient.delete(`/admin/data-management/sources/${sourceType}/profiles/${profileID}`);
}
export async function setActiveSourceProfile(sourceType, profileID) {
    const { data } = await apiClient.post(`/admin/data-management/sources/${sourceType}/profiles/${profileID}/activate`);
    return data;
}
export async function listS3Profiles() {
    const { data } = await apiClient.get('/admin/data-management/s3/profiles');
    return data;
}
export async function createS3Profile(request) {
    const { data } = await apiClient.post('/admin/data-management/s3/profiles', request);
    return data;
}
export async function updateS3Profile(profileID, request) {
    const { data } = await apiClient.put(`/admin/data-management/s3/profiles/${profileID}`, request);
    return data;
}
export async function deleteS3Profile(profileID) {
    await apiClient.delete(`/admin/data-management/s3/profiles/${profileID}`);
}
export async function setActiveS3Profile(profileID) {
    const { data } = await apiClient.post(`/admin/data-management/s3/profiles/${profileID}/activate`);
    return data;
}
export async function createBackupJob(request) {
    const headers = request.idempotency_key
        ? { 'X-Idempotency-Key': request.idempotency_key }
        : undefined;
    const { data } = await apiClient.post('/admin/data-management/backups', request, { headers });
    return data;
}
export async function listBackupJobs(request) {
    const { data } = await apiClient.get('/admin/data-management/backups', {
        params: request
    });
    return data;
}
export async function getBackupJob(jobID) {
    const { data } = await apiClient.get(`/admin/data-management/backups/${jobID}`);
    return data;
}
export const dataManagementAPI = {
    getAgentHealth,
    getConfig,
    updateConfig,
    listSourceProfiles,
    createSourceProfile,
    updateSourceProfile,
    deleteSourceProfile,
    setActiveSourceProfile,
    testS3,
    listS3Profiles,
    createS3Profile,
    updateS3Profile,
    deleteS3Profile,
    setActiveS3Profile,
    createBackupJob,
    listBackupJobs,
    getBackupJob
};
export default dataManagementAPI;
