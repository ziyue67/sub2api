/**
 * Admin Dashboard API endpoints
 * Provides system-wide statistics and metrics
 */
import { apiClient } from '../client';
/**
 * Get dashboard statistics
 * @returns Dashboard statistics including users, keys, accounts, and token usage
 */
export async function getStats() {
    const { data } = await apiClient.get('/admin/dashboard/stats');
    return data;
}
/**
 * Get real-time metrics
 * @returns Real-time system metrics
 */
export async function getRealtimeMetrics() {
    const { data } = await apiClient.get('/admin/dashboard/realtime');
    return data;
}
/**
 * Get usage trend data
 * @param params - Query parameters for filtering
 * @returns Usage trend data
 */
export async function getUsageTrend(params) {
    const { data } = await apiClient.get('/admin/dashboard/trend', { params });
    return data;
}
/**
 * Get model usage statistics
 * @param params - Query parameters for filtering
 * @returns Model usage statistics
 */
export async function getModelStats(params) {
    const { data } = await apiClient.get('/admin/dashboard/models', { params });
    return data;
}
/**
 * Get group usage statistics
 * @param params - Query parameters for filtering
 * @returns Group usage statistics
 */
export async function getGroupStats(params) {
    const { data } = await apiClient.get('/admin/dashboard/groups', { params });
    return data;
}
export async function getUserBreakdown(params) {
    const { data } = await apiClient.get('/admin/dashboard/user-breakdown', {
        params
    });
    return data;
}
/**
 * Get dashboard snapshot v2 (aggregated response for heavy admin pages).
 */
export async function getSnapshotV2(params) {
    const { data } = await apiClient.get('/admin/dashboard/snapshot-v2', {
        params
    });
    return data;
}
/**
 * Get API key usage trend data
 * @param params - Query parameters for filtering
 * @returns API key usage trend data
 */
export async function getApiKeyUsageTrend(params) {
    const { data } = await apiClient.get('/admin/dashboard/api-keys-trend', {
        params
    });
    return data;
}
/**
 * Get user usage trend data
 * @param params - Query parameters for filtering
 * @returns User usage trend data
 */
export async function getUserUsageTrend(params) {
    const { data } = await apiClient.get('/admin/dashboard/users-trend', {
        params
    });
    return data;
}
/**
 * Get user spending ranking data
 * @param params - Query parameters for filtering
 * @returns User spending ranking data
 */
export async function getUserSpendingRanking(params) {
    const { data } = await apiClient.get('/admin/dashboard/users-ranking', {
        params
    });
    return data;
}
/**
 * Get batch usage stats for multiple users
 * @param userIds - Array of user IDs
 * @returns Usage stats map keyed by user ID
 */
export async function getBatchUsersUsage(userIds) {
    const { data } = await apiClient.post('/admin/dashboard/users-usage', {
        user_ids: userIds
    });
    return data;
}
/**
 * Get batch usage stats for multiple API keys
 * @param apiKeyIds - Array of API key IDs
 * @returns Usage stats map keyed by API key ID
 */
export async function getBatchApiKeysUsage(apiKeyIds) {
    const { data } = await apiClient.post('/admin/dashboard/api-keys-usage', {
        api_key_ids: apiKeyIds
    });
    return data;
}
export const dashboardAPI = {
    getStats,
    getRealtimeMetrics,
    getUsageTrend,
    getModelStats,
    getGroupStats,
    getSnapshotV2,
    getApiKeyUsageTrend,
    getUserUsageTrend,
    getUserSpendingRanking,
    getBatchUsersUsage,
    getBatchApiKeysUsage
};
export default dashboardAPI;
