/**
 * Admin Channel Monitor API endpoints
 * Handles channel monitor (uptime/health) management for administrators
 */
import { apiClient } from '../client';
/**
 * List channel monitors with pagination and filters
 */
export async function list(params = {}, options) {
    const { data } = await apiClient.get('/admin/channel-monitors', {
        params,
        signal: options?.signal,
    });
    return data;
}
/**
 * Get a channel monitor by ID
 */
export async function get(id) {
    const { data } = await apiClient.get(`/admin/channel-monitors/${id}`);
    return data;
}
/**
 * Create a new channel monitor
 */
export async function create(params) {
    const { data } = await apiClient.post('/admin/channel-monitors', params);
    return data;
}
/**
 * Duplicate a monitor without exposing its stored API key to the browser.
 * Keep the operation key after ambiguous failures so a retry replays the
 * original server-side operation instead of creating another monitor.
 */
const duplicateOperationKeys = new Map();
function getCurrentAdminID() {
    try {
        const rawUser = globalThis.localStorage?.getItem('auth_user');
        if (!rawUser)
            return null;
        const user = JSON.parse(rawUser);
        if (typeof user !== 'object' || user === null)
            return null;
        const id = user.id;
        if (typeof id !== 'number' || !Number.isSafeInteger(id) || id <= 0)
            return null;
        return String(id);
    }
    catch {
        return null;
    }
}
function duplicateOperationScope(id) {
    const adminID = getCurrentAdminID();
    if (!adminID)
        return null;
    return {
        adminID,
        key: `sub2api:admin:channel-monitor-duplicate:${adminID}:${id}`,
    };
}
function getStoredDuplicateOperationKey(storageKey) {
    try {
        return globalThis.sessionStorage?.getItem(storageKey) ?? null;
    }
    catch {
        return null;
    }
}
function storeDuplicateOperationKey(storageKey, key) {
    try {
        if (key)
            globalThis.sessionStorage?.setItem(storageKey, key);
        else
            globalThis.sessionStorage?.removeItem(storageKey);
    }
    catch {
        // In-memory retry protection still works when browser storage is unavailable.
    }
}
export async function duplicate(id) {
    const scope = duplicateOperationScope(id);
    let idempotencyKey = scope
        ? duplicateOperationKeys.get(scope.key) ?? getStoredDuplicateOperationKey(scope.key)
        : null;
    if (!idempotencyKey) {
        const requestID = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`;
        idempotencyKey = `channel-monitor-duplicate-${scope?.adminID ?? 'unknown-admin'}-${id}-${requestID}`;
    }
    if (scope) {
        duplicateOperationKeys.set(scope.key, idempotencyKey);
        storeDuplicateOperationKey(scope.key, idempotencyKey);
    }
    const { data } = await apiClient.post(`/admin/channel-monitors/${id}/duplicate`, undefined, { headers: { 'Idempotency-Key': idempotencyKey } });
    if (scope) {
        duplicateOperationKeys.delete(scope.key);
        storeDuplicateOperationKey(scope.key, null);
    }
    return data;
}
/**
 * Update an existing channel monitor.
 * api_key field: empty string means "do not modify".
 */
export async function update(id, params) {
    const { data } = await apiClient.put(`/admin/channel-monitors/${id}`, params);
    return data;
}
/**
 * Delete a channel monitor
 */
export async function del(id) {
    await apiClient.delete(`/admin/channel-monitors/${id}`);
}
/**
 * Trigger an immediate manual check for a channel monitor.
 * Returns the latest check results for primary + extra models.
 */
export async function runNow(id) {
    const { data } = await apiClient.post(`/admin/channel-monitors/${id}/run`);
    return data;
}
/**
 * List historical check results for a monitor.
 */
export async function listHistory(id, params = {}) {
    const { data } = await apiClient.get(`/admin/channel-monitors/${id}/history`, { params });
    return data;
}
export const channelMonitorAPI = {
    list,
    get,
    create,
    duplicate,
    update,
    del,
    runNow,
    listHistory,
};
export default channelMonitorAPI;
