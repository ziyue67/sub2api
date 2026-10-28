/**
 * Admin Groups API endpoints
 * Handles API key group management for administrators
 */
import { apiClient } from '../client';
/**
 * List all groups with pagination
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @param filters - Optional filters (platform, status, is_exclusive, search)
 * @returns Paginated list of groups
 */
export async function list(page = 1, pageSize = 20, filters, options) {
    const { data } = await apiClient.get('/admin/groups', {
        params: {
            page,
            page_size: pageSize,
            ...filters
        },
        signal: options?.signal
    });
    return data;
}
/**
 * Get all active groups (without pagination)
 * @param platform - Optional platform filter
 * @returns List of all active groups
 */
export async function getAll(platform) {
    const { data } = await apiClient.get('/admin/groups/all', {
        params: platform ? { platform } : undefined
    });
    return data;
}
/**
 * Get ALL groups including disabled ones — used by the API Key group filter so
 * that admins can filter users whose keys are still bound to a now-disabled group.
 */
export async function getAllIncludingInactive() {
    const { data } = await apiClient.get('/admin/groups/all', {
        params: { include_inactive: true }
    });
    return data;
}
/**
 * Get active groups by platform
 * @param platform - Platform to filter by
 * @returns List of groups for the specified platform
 */
export async function getByPlatform(platform) {
    return getAll(platform);
}
/** 获取当前 Sub2API 服务端的 Live 运行环境能力。 */
export async function getLiveCapability() {
    const { data } = await apiClient.get('/admin/groups/live-capability');
    return data;
}
/**
 * Get group by ID
 * @param id - Group ID
 * @returns Group details
 */
export async function getById(id) {
    const { data } = await apiClient.get(`/admin/groups/${id}`);
    return data;
}
/**
 * Get candidate models for the group model allowlist.
 * id=0 returns platform default models for create flow.
 */
export async function getModelAllowlistCandidates(id, platform) {
    const { data } = await apiClient.get(`/admin/groups/${id}/model-allowlist-candidates`, {
        params: platform ? { platform } : undefined
    });
    return data.models || [];
}
/**
 * Create new group
 * @param groupData - Group data
 * @returns Created group
 */
export async function create(groupData) {
    const { data } = await apiClient.post('/admin/groups', groupData);
    return data;
}
/**
 * Duplicate a group on the server so configuration that is not present in the
 * list response is preserved. Keep the operation key after ambiguous failures
 * so a retry replays the original operation instead of creating another group.
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
        key: `sub2api:admin:group-duplicate:${adminID}:${id}`
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
        idempotencyKey = `group-duplicate-${scope?.adminID ?? 'unknown-admin'}-${id}-${requestID}`;
    }
    if (scope) {
        duplicateOperationKeys.set(scope.key, idempotencyKey);
        storeDuplicateOperationKey(scope.key, idempotencyKey);
    }
    const { data } = await apiClient.post(`/admin/groups/${id}/duplicate`, undefined, {
        headers: { 'Idempotency-Key': idempotencyKey }
    });
    if (scope) {
        duplicateOperationKeys.delete(scope.key);
        storeDuplicateOperationKey(scope.key, null);
    }
    return data;
}
/**
 * Update group
 * @param id - Group ID
 * @param updates - Fields to update
 * @returns Updated group
 */
export async function update(id, updates) {
    const { data } = await apiClient.put(`/admin/groups/${id}`, updates);
    return data;
}
/**
 * Delete group
 * @param id - Group ID
 * @returns Success confirmation
 */
export async function deleteGroup(id) {
    const { data } = await apiClient.delete(`/admin/groups/${id}`);
    return data;
}
/**
 * Toggle group status
 * @param id - Group ID
 * @param status - New status
 * @returns Updated group
 */
export async function toggleStatus(id, status) {
    return update(id, { status });
}
/**
 * Get group statistics
 * @param id - Group ID
 * @returns Group usage statistics
 */
export async function getStats(id) {
    const { data } = await apiClient.get(`/admin/groups/${id}/stats`);
    return data;
}
/**
 * Get API keys in a group
 * @param id - Group ID
 * @param page - Page number
 * @param pageSize - Items per page
 * @returns Paginated list of API keys in the group
 */
export async function getGroupApiKeys(id, page = 1, pageSize = 20) {
    const { data } = await apiClient.get(`/admin/groups/${id}/api-keys`, {
        params: { page, page_size: pageSize }
    });
    return data;
}
export async function listCompositeRoutes(id) {
    const { data } = await apiClient.get(`/admin/groups/${id}/composite-routes`);
    return data;
}
export async function createCompositeRoute(id, route) {
    const { data } = await apiClient.post(`/admin/groups/${id}/composite-routes`, route);
    return data;
}
export async function updateCompositeRoute(id, routeId, route) {
    const { data } = await apiClient.put(`/admin/groups/${id}/composite-routes/${routeId}`, route);
    return data;
}
export async function deleteCompositeRoute(id, routeId) {
    const { data } = await apiClient.delete(`/admin/groups/${id}/composite-routes/${routeId}`);
    return data;
}
export async function previewCompositeRoute(id, request) {
    const { data } = await apiClient.post(`/admin/groups/${id}/composite-routes/preview`, request);
    return data;
}
/**
 * Get rate multipliers for users in a group
 * @param id - Group ID
 * @returns List of user rate multiplier entries
 */
export async function getGroupRateMultipliers(id) {
    const { data } = await apiClient.get(`/admin/groups/${id}/rate-multipliers`);
    return data;
}
/**
 * Update group sort orders
 * @param updates - Array of { id, sort_order } objects
 * @returns Success confirmation
 */
export async function updateSortOrder(updates) {
    const { data } = await apiClient.put('/admin/groups/sort-order', {
        updates
    });
    return data;
}
/**
 * Clear all rate multipliers for a group
 * @param id - Group ID
 * @returns Success confirmation
 */
export async function clearGroupRateMultipliers(id) {
    const { data } = await apiClient.delete(`/admin/groups/${id}/rate-multipliers`);
    return data;
}
/**
 * Batch set rate multipliers for users in a group
 * Only touches rate_multiplier column; preserves rpm_override on existing rows.
 */
export async function batchSetGroupRateMultipliers(id, entries) {
    const { data } = await apiClient.put(`/admin/groups/${id}/rate-multipliers`, { entries });
    return data;
}
/**
 * Get RPM overrides for users in a group (subset of rate-multipliers endpoint).
 */
export async function getGroupRPMOverrides(id) {
    const { data } = await apiClient.get(`/admin/groups/${id}/rate-multipliers`);
    return data
        .filter(e => e.rpm_override != null)
        .map(e => ({
        user_id: e.user_id,
        user_name: e.user_name,
        user_email: e.user_email,
        user_notes: e.user_notes,
        user_status: e.user_status,
        rpm_override: e.rpm_override
    }));
}
/**
 * Batch set RPM overrides for users in a group.
 * Only touches rpm_override column; preserves rate_multiplier on existing rows.
 */
export async function batchSetGroupRPMOverrides(id, entries) {
    const { data } = await apiClient.put(`/admin/groups/${id}/rpm-overrides`, { entries });
    return data;
}
/**
 * Clear all RPM overrides for a group (preserves rate_multiplier).
 */
export async function clearGroupRPMOverrides(id) {
    const { data } = await apiClient.delete(`/admin/groups/${id}/rpm-overrides`);
    return data;
}
/**
 * Get the models each user may not use in a group (subset of rate-multipliers endpoint).
 */
export async function getGroupUserDeniedModels(id) {
    const { data } = await apiClient.get(`/admin/groups/${id}/rate-multipliers`);
    return data
        .filter(e => (e.denied_models?.length ?? 0) > 0)
        .map(e => ({
        user_id: e.user_id,
        user_name: e.user_name,
        user_email: e.user_email,
        user_notes: e.user_notes,
        user_status: e.user_status,
        denied_models: [...(e.denied_models ?? [])]
    }));
}
/**
 * Replace the per-user denied models of a group; users not listed become unrestricted.
 */
export async function batchSetGroupUserDeniedModels(id, entries) {
    const { data } = await apiClient.put(`/admin/groups/${id}/user-denied-models`, { entries });
    return data;
}
/**
 * Clear the denied models of every user in a group.
 */
export async function clearGroupUserDeniedModels(id) {
    const { data } = await apiClient.delete(`/admin/groups/${id}/user-denied-models`);
    return data;
}
/**
 * Get usage summary (today + yesterday + cumulative cost) for all groups
 * @returns Array of group usage summaries
 */
export async function getUsageSummary() {
    const { data } = await apiClient.get('/admin/groups/usage-summary');
    return data;
}
/**
 * Get capacity summary (concurrency/sessions/RPM) for all active groups
 */
export async function getCapacitySummary() {
    const { data } = await apiClient.get('/admin/groups/capacity-summary');
    return data;
}
export const groupsAPI = {
    list,
    getAll,
    getByPlatform,
    getAllIncludingInactive,
    getLiveCapability,
    getById,
    getModelAllowlistCandidates,
    create,
    duplicate,
    update,
    delete: deleteGroup,
    toggleStatus,
    getStats,
    getGroupApiKeys,
    listCompositeRoutes,
    createCompositeRoute,
    updateCompositeRoute,
    deleteCompositeRoute,
    previewCompositeRoute,
    getGroupRateMultipliers,
    clearGroupRateMultipliers,
    batchSetGroupRateMultipliers,
    getGroupRPMOverrides,
    clearGroupRPMOverrides,
    batchSetGroupRPMOverrides,
    getGroupUserDeniedModels,
    batchSetGroupUserDeniedModels,
    clearGroupUserDeniedModels,
    updateSortOrder,
    getUsageSummary,
    getCapacitySummary
};
export default groupsAPI;
