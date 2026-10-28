/**
 * Admin Proxies API endpoints
 * Handles proxy server management for administrators
 */
import { apiClient } from '../client';
function assertProxyArray(value) {
    if (!Array.isArray(value)) {
        throw new Error('Invalid proxy list response');
    }
}
/**
 * List all proxies with pagination
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @param filters - Optional filters
 * @returns Paginated list of proxies
 */
export async function list(page = 1, pageSize = 20, filters, options) {
    const { data } = await apiClient.get('/admin/proxies', {
        params: {
            page,
            page_size: pageSize,
            ...filters
        },
        signal: options?.signal
    });
    assertProxyArray(data?.items);
    return data;
}
/**
 * Get all active proxies (without pagination)
 * @returns List of all active proxies
 */
export async function getAll() {
    const { data } = await apiClient.get('/admin/proxies/all');
    assertProxyArray(data);
    return data;
}
/**
 * Get all active proxies with account count (sorted by creation time desc)
 * @returns List of all active proxies with account count
 */
export async function getAllWithCount() {
    const { data } = await apiClient.get('/admin/proxies/all', {
        params: { with_count: 'true' }
    });
    assertProxyArray(data);
    return data;
}
/**
 * Get proxy by ID
 * @param id - Proxy ID
 * @returns Proxy details
 */
export async function getById(id) {
    const { data } = await apiClient.get(`/admin/proxies/${id}`);
    return data;
}
/**
 * Create new proxy
 * @param proxyData - Proxy data
 * @returns Created proxy
 */
export async function create(proxyData) {
    const { data } = await apiClient.post('/admin/proxies', proxyData);
    return data;
}
/**
 * Update proxy
 * @param id - Proxy ID
 * @param updates - Fields to update
 * @returns Updated proxy
 */
export async function update(id, updates) {
    const { data } = await apiClient.put(`/admin/proxies/${id}`, updates);
    return data;
}
/**
 * Delete proxy
 * @param id - Proxy ID
 * @returns Success confirmation
 */
export async function deleteProxy(id) {
    const { data } = await apiClient.delete(`/admin/proxies/${id}`);
    return data;
}
/**
 * Toggle proxy status
 * @param id - Proxy ID
 * @param status - New status
 * @returns Updated proxy
 */
export async function toggleStatus(id, status) {
    return update(id, { status });
}
/**
 * Test proxy connectivity
 * @param id - Proxy ID
 * @returns Test result with IP info
 */
export async function testProxy(id) {
    const { data } = await apiClient.post(`/admin/proxies/${id}/test`);
    return data;
}
/**
 * Check proxy quality across common AI targets
 * @param id - Proxy ID
 * @returns Quality check result
 */
export async function checkProxyQuality(id) {
    const { data } = await apiClient.post(`/admin/proxies/${id}/quality-check`);
    return data;
}
/**
 * Get proxy usage statistics
 * @param id - Proxy ID
 * @returns Proxy usage statistics
 */
export async function getStats(id) {
    const { data } = await apiClient.get(`/admin/proxies/${id}/stats`);
    return data;
}
/**
 * Get accounts using a proxy
 * @param id - Proxy ID
 * @returns List of accounts using the proxy
 */
export async function getProxyAccounts(id) {
    const { data } = await apiClient.get(`/admin/proxies/${id}/accounts`);
    return data;
}
/**
 * Batch create proxies
 * @param proxies - Array of proxy data to create
 * @returns Creation result with count of created and skipped
 */
export async function batchCreate(proxies) {
    const { data } = await apiClient.post('/admin/proxies/batch', { proxies });
    return data;
}
export async function batchDelete(ids) {
    const { data } = await apiClient.post('/admin/proxies/batch-delete', { ids });
    return data;
}
export async function exportData(options) {
    const params = {};
    if (options?.ids && options.ids.length > 0) {
        params.ids = options.ids.join(',');
    }
    else if (options?.filters) {
        const { protocol, status, search, sort_by, sort_order } = options.filters;
        if (protocol)
            params.protocol = protocol;
        if (status)
            params.status = status;
        if (search)
            params.search = search;
        if (sort_by)
            params.sort_by = sort_by;
        if (sort_order)
            params.sort_order = sort_order;
    }
    const { data } = await apiClient.get('/admin/proxies/data', { params });
    return data;
}
export async function importData(payload) {
    const { data } = await apiClient.post('/admin/proxies/data', payload);
    return data;
}
export const proxiesAPI = {
    list,
    getAll,
    getAllWithCount,
    getById,
    create,
    update,
    delete: deleteProxy,
    toggleStatus,
    testProxy,
    checkProxyQuality,
    getStats,
    getProxyAccounts,
    batchCreate,
    batchDelete,
    exportData,
    importData
};
export default proxiesAPI;
