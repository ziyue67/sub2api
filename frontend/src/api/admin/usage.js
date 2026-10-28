/**
 * Admin Usage API endpoints
 * Handles admin-level usage logs and statistics retrieval
 */
import { apiClient } from '../client';
// ==================== API Functions ====================
/**
 * List all usage logs with optional filters (admin only)
 * @param params - Query parameters for filtering and pagination
 * @returns Paginated list of usage logs
 */
export async function list(params, options) {
    const { data } = await apiClient.get('/admin/usage', {
        params,
        signal: options?.signal
    });
    return data;
}
/**
 * Get usage statistics with optional filters (admin only)
 * @param params - Query parameters for filtering
 * @returns Usage statistics
 */
export async function getStats(params) {
    const { data } = await apiClient.get('/admin/usage/stats', {
        params
    });
    return data;
}
/**
 * Search users by email keyword (admin only)
 * @param keyword - Email keyword to search
 * @returns List of matching users (max 30)
 */
export async function searchUsers(keyword) {
    const { data } = await apiClient.get('/admin/usage/search-users', {
        params: { q: keyword }
    });
    return data;
}
/**
 * Search API keys by user ID and/or keyword (admin only)
 * @param userId - Optional user ID to filter by
 * @param keyword - Optional keyword to search in key name
 * @returns List of matching API keys (max 30)
 */
export async function searchApiKeys(userId, keyword) {
    const params = {};
    if (userId !== undefined) {
        params.user_id = userId;
    }
    if (keyword) {
        params.q = keyword;
    }
    const { data } = await apiClient.get('/admin/usage/search-api-keys', {
        params
    });
    return data;
}
/**
 * List usage cleanup tasks (admin only)
 * @param params - Query parameters for pagination
 * @returns Paginated list of cleanup tasks
 */
export async function listCleanupTasks(params, options) {
    const { data } = await apiClient.get('/admin/usage/cleanup-tasks', {
        params,
        signal: options?.signal
    });
    return data;
}
/**
 * Create a usage cleanup task (admin only)
 * @param payload - Cleanup task parameters
 * @returns Created cleanup task
 */
export async function createCleanupTask(payload) {
    const { data } = await apiClient.post('/admin/usage/cleanup-tasks', payload);
    return data;
}
/**
 * Cancel a usage cleanup task (admin only)
 * @param taskId - Task ID to cancel
 */
export async function cancelCleanupTask(taskId) {
    const { data } = await apiClient.post(`/admin/usage/cleanup-tasks/${taskId}/cancel`);
    return data;
}
export const adminUsageAPI = {
    list,
    getStats,
    searchUsers,
    searchApiKeys,
    listCleanupTasks,
    createCleanupTask,
    cancelCleanupTask
};
export default adminUsageAPI;
