import { apiClient } from '../client';
export const imageArchiveAPI = {
    async list(params, signal) {
        const { data } = await apiClient.get('/admin/image-archive', { params, signal });
        return data;
    },
    async stats() {
        const { data } = await apiClient.get('/admin/image-archive/stats');
        return data;
    },
    async configure(config) {
        const { data } = await apiClient.put('/admin/image-archive/config', config);
        return data;
    },
    async get(id, signal) {
        const { data } = await apiClient.get(`/admin/image-archive/${id}`, { signal });
        return data;
    },
    async remove(id, fileID) {
        await apiClient.delete(fileID === undefined ? `/admin/image-archive/${id}` : `/admin/image-archive/${id}/files/${fileID}`);
    },
    async file(id, fileID, thumbnail, signal) {
        const { data } = await apiClient.get(`/admin/image-archive/${id}/files/${fileID}`, {
            params: { thumbnail }, responseType: 'blob', signal,
        });
        return data;
    },
};
