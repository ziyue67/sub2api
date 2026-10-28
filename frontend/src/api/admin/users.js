/**
 * Admin Users API endpoints
 * Handles user management for administrators
 */
import { apiClient } from '../client';
import { listPlatformIds } from '@/constants/platformCatalog';
/**
 * List all users with pagination
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @param filters - Optional filters (status, role, search, attributes)
 * @param options - Optional request options (signal)
 * @returns Paginated list of users
 */
export async function list(page = 1, pageSize = 20, filters, options) {
    // Build params with attribute filters in attr[id]=value format
    const params = {
        page,
        page_size: pageSize,
        status: filters?.status,
        role: filters?.role,
        search: filters?.search,
        group_name: filters?.group_name,
        api_key_group_id: filters?.api_key_group_id,
        include_subscriptions: filters?.include_subscriptions,
        sort_by: filters?.sort_by,
        sort_order: filters?.sort_order
    };
    // Add attribute filters as attr[id]=value
    if (filters?.attributes) {
        for (const [attrId, value] of Object.entries(filters.attributes)) {
            if (value) {
                params[`attr[${attrId}]`] = value;
            }
        }
    }
    const { data } = await apiClient.get('/admin/users', {
        params,
        signal: options?.signal
    });
    return data;
}
/**
 * Get user by ID
 * @param id - User ID
 * @param includeDeleted - Whether to include soft-deleted users
 * @returns User details
 */
export async function getById(id, includeDeleted = false) {
    const url = includeDeleted ? `/admin/users/${id}?include_deleted=true` : `/admin/users/${id}`;
    const { data } = await apiClient.get(url);
    return data;
}
/**
 * Create new user
 * @param userData - User data (email, password, etc.)
 * @returns Created user
 */
export async function create(userData) {
    const { data } = await apiClient.post('/admin/users', userData);
    return data;
}
/**
 * Update user
 * @param id - User ID
 * @param updates - Fields to update
 * @returns Updated user
 */
export async function update(id, updates) {
    const { data } = await apiClient.put(`/admin/users/${id}`, updates);
    return data;
}
/**
 * Delete user
 * @param id - User ID
 * @returns Success confirmation
 */
export async function deleteUser(id) {
    const { data } = await apiClient.delete(`/admin/users/${id}`);
    return data;
}
/**
 * Update user balance
 * @param id - User ID
 * @param balance - New balance
 * @param operation - Operation type ('set', 'add', 'subtract')
 * @param notes - Optional notes for the balance adjustment
 * @returns Updated user
 */
export async function updateBalance(id, balance, operation = 'set', notes) {
    const { data } = await apiClient.post(`/admin/users/${id}/balance`, {
        balance,
        operation,
        notes: notes || ''
    });
    return data;
}
/**
 * Update user concurrency
 * @param id - User ID
 * @param concurrency - New concurrency limit
 * @returns Updated user
 */
export async function updateConcurrency(id, concurrency) {
    return update(id, { concurrency });
}
/** Overwrite concurrency and/or RPM limits for multiple users in one request. */
export async function batchUpdateLimits(request) {
    const { data } = await apiClient.post('/admin/users/batch-limits', request);
    return data;
}
/**
 * Toggle user status
 * @param id - User ID
 * @param status - New status
 * @returns Updated user
 */
export async function toggleStatus(id, status) {
    return update(id, { status });
}
/**
 * Get user's API keys
 * @param id - User ID
 * @returns List of user's API keys
 */
export async function getUserApiKeys(id) {
    const { data } = await apiClient.get(`/admin/users/${id}/api-keys`);
    return data;
}
/**
 * Get user's usage statistics
 * @param id - User ID
 * @param period - Time period
 * @returns User usage statistics
 */
export async function getUserUsageStats(id, period = 'month') {
    const { data } = await apiClient.get(`/admin/users/${id}/usage`, {
        params: { period }
    });
    return data;
}
/**
 * Get user's balance/concurrency change history
 * @param id - User ID
 * @param page - Page number
 * @param pageSize - Items per page
 * @param type - Optional type filter (balance, affiliate_balance, admin_balance, concurrency, admin_concurrency, subscription)
 * @returns Paginated balance history with total_recharged
 */
export async function getUserBalanceHistory(id, page = 1, pageSize = 20, type) {
    const params = { page, page_size: pageSize };
    if (type)
        params.type = type;
    const { data } = await apiClient.get(`/admin/users/${id}/balance-history`, { params });
    return data;
}
/**
 * Replace user's exclusive group
 * @param userId - User ID
 * @param oldGroupId - Current group ID to replace
 * @param newGroupId - New group ID to replace with
 * @returns Number of migrated keys
 */
export async function replaceGroup(userId, oldGroupId, newGroupId) {
    const { data } = await apiClient.post(`/admin/users/${userId}/replace-group`, { old_group_id: oldGroupId, new_group_id: newGroupId });
    return data;
}
export async function bindUserAuthIdentity(userId, input) {
    const { data } = await apiClient.post(`/admin/users/${userId}/auth-identities`, input);
    return data;
}
/**
 * Platform quota types
 */
// 与后端 AllowedQuotaPlatforms 同源：平台清单中的全部具体平台。
export function platformQuotaPlatforms() {
    return listPlatformIds();
}
/**
 * Get user's platform quotas
 */
export async function getPlatformQuotas(id) {
    const { data } = await apiClient.get(`/admin/users/${id}/platform-quotas`);
    return data;
}
/**
 * Replace user's platform quotas (全量替换)
 */
export async function updatePlatformQuotas(id, quotas) {
    const { data } = await apiClient.put(`/admin/users/${id}/platform-quotas`, { quotas });
    return data;
}
/**
 * Reset a single (platform, window) usage immediately
 */
export async function resetPlatformQuotaWindow(id, platform, window) {
    const { data } = await apiClient.post(`/admin/users/${id}/platform-quotas/reset`, { platform, window });
    return data;
}
export const usersAPI = {
    list,
    getById,
    create,
    update,
    delete: deleteUser,
    updateBalance,
    updateConcurrency,
    batchUpdateLimits,
    toggleStatus,
    getUserApiKeys,
    getUserUsageStats,
    getUserBalanceHistory,
    replaceGroup,
    bindUserAuthIdentity,
    getPlatformQuotas,
    updatePlatformQuotas,
    resetPlatformQuotaWindow,
};
export default usersAPI;
