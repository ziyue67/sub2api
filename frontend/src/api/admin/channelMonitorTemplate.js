/**
 * Admin Channel Monitor Request Template API.
 *
 * 模板 = 一组可复用的 headers + 可选 body 覆盖配置。
 * 应用到监控 = 拷贝快照；模板后续变动不自动同步，需手动点「应用到关联监控」刷新。
 */
import { apiClient } from '../client';
export async function list(params = {}) {
    const { data } = await apiClient.get('/admin/channel-monitor-templates', {
        params,
    });
    return data;
}
export async function get(id) {
    const { data } = await apiClient.get(`/admin/channel-monitor-templates/${id}`);
    return data;
}
export async function create(params) {
    const { data } = await apiClient.post('/admin/channel-monitor-templates', params);
    return data;
}
export async function update(id, params) {
    const { data } = await apiClient.put(`/admin/channel-monitor-templates/${id}`, params);
    return data;
}
export async function del(id) {
    await apiClient.delete(`/admin/channel-monitor-templates/${id}`);
}
/**
 * Apply the template to the specified associated monitors (overwrite snapshot fields).
 * monitorIds must be a non-empty subset of the template's associated monitors.
 * Returns count of actually affected monitors.
 */
export async function apply(id, monitorIds) {
    const { data } = await apiClient.post(`/admin/channel-monitor-templates/${id}/apply`, { monitor_ids: monitorIds });
    return data;
}
/**
 * List monitors currently associated to this template (used by apply picker).
 */
export async function listAssociatedMonitors(id) {
    const { data } = await apiClient.get(`/admin/channel-monitor-templates/${id}/monitors`);
    return data;
}
export const channelMonitorTemplateAPI = {
    list,
    get,
    create,
    update,
    del,
    apply,
    listAssociatedMonitors,
};
export default channelMonitorTemplateAPI;
