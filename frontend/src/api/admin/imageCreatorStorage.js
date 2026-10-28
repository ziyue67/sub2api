import { apiClient } from '../client';
export async function getStorageGovernanceStats() {
    const { data } = await apiClient.get('/admin/image-creator/storage-governance');
    return data;
}
export async function cleanupStorageGovernance(action) {
    const { data } = await apiClient.post('/admin/image-creator/storage-governance', { action });
    return data;
}
export const imageCreatorStorageAPI = {
    getStorageGovernanceStats,
    cleanupStorageGovernance,
};
export default imageCreatorStorageAPI;
