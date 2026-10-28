/**
 * API Keys management endpoints
 * Handles CRUD operations for user API keys
 */
import { apiClient } from './client';
/**
 * List all API keys for current user
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 10)
 * @param filters - Optional filter parameters
 * @param options - Optional request options
 * @returns Paginated list of API keys
 */
export async function list(page = 1, pageSize = 10, filters, options) {
    const { data } = await apiClient.get('/keys', {
        params: { page, page_size: pageSize, ...filters },
        signal: options?.signal
    });
    return data;
}
/**
 * Get API key by ID
 * @param id - API key ID
 * @returns API key details
 */
export async function getById(id) {
    const { data } = await apiClient.get(`/keys/${id}`);
    return data;
}
/**
 * Create new API key
 * @param name - Key name
 * @param groupId - Optional group ID
 * @param customKey - Optional custom key value
 * @param ipWhitelist - Optional IP whitelist
 * @param ipBlacklist - Optional IP blacklist
 * @param quota - Optional quota limit in USD (0 = unlimited)
 * @param expiresInDays - Optional days until expiry (undefined = never expires)
 * @param rateLimitData - Optional rate limit fields
 * @param concurrencyLimit - Concurrent request limit (0 = no additional key limit)
 * @returns Created API key
 */
export async function create(name, groupId, customKey, ipWhitelist, ipBlacklist, quota, expiresInDays, rateLimitData, concurrencyLimit = 0, groupRoutes, groupIds) {
    const payload = { name, concurrency_limit: concurrencyLimit };
    if (groupIds !== undefined)
        payload.group_ids = groupIds;
    if (groupId !== undefined) {
        payload.group_id = groupId;
    }
    if (groupRoutes !== undefined) {
        payload.group_routes = groupRoutes;
    }
    if (customKey) {
        payload.custom_key = customKey;
    }
    if (ipWhitelist && ipWhitelist.length > 0) {
        payload.ip_whitelist = ipWhitelist;
    }
    if (ipBlacklist && ipBlacklist.length > 0) {
        payload.ip_blacklist = ipBlacklist;
    }
    if (quota !== undefined && quota > 0) {
        payload.quota = quota;
    }
    if (expiresInDays !== undefined && expiresInDays > 0) {
        payload.expires_in_days = expiresInDays;
    }
    if (rateLimitData?.rate_limit_5h && rateLimitData.rate_limit_5h > 0) {
        payload.rate_limit_5h = rateLimitData.rate_limit_5h;
    }
    if (rateLimitData?.rate_limit_1d && rateLimitData.rate_limit_1d > 0) {
        payload.rate_limit_1d = rateLimitData.rate_limit_1d;
    }
    if (rateLimitData?.rate_limit_7d && rateLimitData.rate_limit_7d > 0) {
        payload.rate_limit_7d = rateLimitData.rate_limit_7d;
    }
    const { data } = await apiClient.post('/keys', payload);
    return data;
}
/**
 * Update API key
 * @param id - API key ID
 * @param updates - Fields to update
 * @returns Updated API key
 */
export async function update(id, updates) {
    const { data } = await apiClient.put(`/keys/${id}`, updates);
    return data;
}
/** Reuse per-key validation and permissions, with at most five requests in flight. */
export async function bulkUpdate(ids, updates) {
    const uniqueIds = [...new Set(ids)];
    const result = { succeededIds: [], failures: [] };
    for (let offset = 0; offset < uniqueIds.length; offset += 5) {
        const batch = uniqueIds.slice(offset, offset + 5);
        const responses = await Promise.allSettled(batch.map((id) => update(id, updates)));
        responses.forEach((response, index) => {
            if (response.status === 'fulfilled') {
                result.succeededIds.push(batch[index]);
            }
            else {
                result.failures.push({ id: batch[index], error: response.reason });
            }
        });
    }
    return result;
}
/**
 * Delete API key
 * @param id - API key ID
 * @returns Success confirmation
 */
export async function deleteKey(id) {
    const { data } = await apiClient.delete(`/keys/${id}`);
    return data;
}
/**
 * Toggle API key status (active/inactive)
 * @param id - API key ID
 * @param status - New status
 * @returns Updated API key
 */
export async function toggleStatus(id, status) {
    return update(id, { status });
}
/** Read actual queue policy and counts; omitting IDs requests policy only. */
export async function getConcurrency(ids = [], options) {
    const { data } = await apiClient.get('/keys/concurrency', {
        params: ids.length ? { ids: ids.join(',') } : {},
        signal: options?.signal,
    });
    return data;
}
export const keysAPI = {
    getConcurrency,
    list,
    getById,
    create,
    update,
    bulkUpdate,
    delete: deleteKey,
    toggleStatus
};
export default keysAPI;
