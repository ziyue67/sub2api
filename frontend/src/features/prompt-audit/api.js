import { apiClient } from '@/api/client';
import { eventFilterPayload, eventQueryParams } from './viewModel';
const basePath = '/admin/prompt-audit';
export async function getConfig() {
    const { data } = await apiClient.get(`${basePath}/config`);
    return data;
}
export async function updateConfig(payload) {
    const { data } = await apiClient.put(`${basePath}/config`, payload);
    return data;
}
export async function probeEndpoint(endpoint) {
    const { data } = await apiClient.post(`${basePath}/endpoints/probe`, {
        endpoint: {
            id: endpoint.id,
            name: endpoint.name,
            protocol: 'openai_compatible',
            base_url: endpoint.base_url,
            model: endpoint.model,
            token: endpoint.token || undefined,
            timeout_ms: endpoint.timeout_ms,
            input_limit: endpoint.input_limit,
            enabled: endpoint.enabled,
        },
    });
    return data;
}
export async function getRuntime() {
    const { data } = await apiClient.get(`${basePath}/runtime`);
    return data;
}
export async function listEvents(filters, page, pageSize) {
    const { data } = await apiClient.get(`${basePath}/events`, {
        params: { page, page_size: pageSize, ...eventQueryParams(filters) },
    });
    return data;
}
export async function getEvent(id) {
    const { data } = await apiClient.get(`${basePath}/events/${id}`);
    return data;
}
export async function deleteEvent(id) {
    const { data } = await apiClient.delete(`${basePath}/events/${id}`);
    return data;
}
export async function batchDeleteEvents(ids) {
    const { data } = await apiClient.post(`${basePath}/events/batch-delete`, { ids });
    return data;
}
export async function previewDelete(filters) {
    const { data } = await apiClient.post(`${basePath}/events/delete-preview`, eventFilterPayload(filters));
    return data;
}
export async function deleteEventsByFilter(filters, preview) {
    const { data } = await apiClient.post(`${basePath}/events/delete-by-filter`, {
        filter: eventFilterPayload(filters),
        snapshot_max_id: preview.snapshot_max_id,
        filter_hash: preview.filter_hash,
        confirmation_token: preview.confirmation_token,
        confirm: true,
    });
    return data;
}
export async function listGroups() {
    const { data } = await apiClient.get('/admin/groups/all', {
        params: { include_inactive: true },
    });
    return data;
}
export const promptAuditAPI = {
    getConfig,
    updateConfig,
    probeEndpoint,
    getRuntime,
    listEvents,
    getEvent,
    deleteEvent,
    batchDeleteEvents,
    previewDelete,
    deleteEventsByFilter,
    listGroups,
};
export default promptAuditAPI;
