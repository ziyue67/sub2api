import { apiClient } from '../client';
export async function list() {
    const { data } = await apiClient.get('/admin/plugins');
    return data;
}
export async function upload(file) {
    const form = new FormData();
    form.append('plugin', file);
    const { data } = await apiClient.post('/admin/plugins/upload', form, {
        headers: { 'Content-Type': 'multipart/form-data' },
        timeout: 120000
    });
    return data;
}
export async function enable(id, rolloutPercent, acceptUntested) {
    const { data } = await apiClient.post(`/admin/plugins/${id}/enable`, {
        rollout_percent: rolloutPercent,
        accept_untested: acceptUntested
    });
    return data;
}
export async function disable(id) {
    const { data } = await apiClient.post(`/admin/plugins/${id}/disable`);
    return data;
}
export async function remove(id) {
    await apiClient.delete(`/admin/plugins/${id}`);
}
export async function getConfig(id) {
    const { data } = await apiClient.get(`/admin/plugins/${id}/config`);
    return data;
}
export async function saveConfig(id, config) {
    const { data } = await apiClient.put(`/admin/plugins/${id}/config`, config);
    return data;
}
export async function test(id) {
    const { data } = await apiClient.post(`/admin/plugins/${id}/test`);
    return data;
}
export async function status(id) {
    const { data } = await apiClient.get(`/admin/plugins/${id}/status`);
    return data;
}
export async function createUISession(id) {
    const { data } = await apiClient.post(`/admin/plugins/${id}/ui-session`);
    return data;
}
export default {
    list,
    upload,
    enable,
    disable,
    remove,
    getConfig,
    saveConfig,
    test,
    status,
    createUISession
};
