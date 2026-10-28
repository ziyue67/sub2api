import { apiClient } from '../client';
// S3 Config
export async function getS3Config() {
    const { data } = await apiClient.get('/admin/backups/s3-config');
    return data;
}
export async function updateS3Config(config) {
    const { data } = await apiClient.put('/admin/backups/s3-config', config);
    return data;
}
export async function testS3Connection(config) {
    const { data } = await apiClient.post('/admin/backups/s3-config/test', config);
    return data;
}
export async function getImageStorageConfig() {
    const { data } = await apiClient.get('/admin/backups/image-storage');
    return data;
}
export async function updateImageStorageConfig(config) {
    const { data } = await apiClient.put('/admin/backups/image-storage', config);
    return data;
}
export async function testImageStorageConnection(config) {
    const { data } = await apiClient.post('/admin/backups/image-storage/test', config);
    return data;
}
// Schedule
export async function getSchedule() {
    const { data } = await apiClient.get('/admin/backups/schedule');
    return data;
}
export async function updateSchedule(config) {
    const { data } = await apiClient.put('/admin/backups/schedule', config);
    return data;
}
// Backup operations
export async function createBackup(req) {
    const { data } = await apiClient.post('/admin/backups', req || {});
    return data;
}
export async function listBackups() {
    const { data } = await apiClient.get('/admin/backups');
    return data;
}
export async function getBackup(id) {
    const { data } = await apiClient.get(`/admin/backups/${id}`);
    return data;
}
export async function deleteBackup(id, deleteArchived = false) {
    await apiClient.delete(`/admin/backups/${id}`, deleteArchived ? { params: { delete_archived: true } } : undefined);
}
export async function getDownloadURL(id) {
    const { data } = await apiClient.get(`/admin/backups/${id}/download-url`);
    return data;
}
// Restore
export async function restoreBackup(id, password) {
    const { data } = await apiClient.post(`/admin/backups/${id}/restore`, { password });
    return data;
}
export const backupAPI = {
    getS3Config,
    updateS3Config,
    testS3Connection,
    getImageStorageConfig,
    updateImageStorageConfig,
    testImageStorageConnection,
    getSchedule,
    updateSchedule,
    createBackup,
    listBackups,
    getBackup,
    deleteBackup,
    getDownloadURL,
    restoreBackup,
};
export default backupAPI;
