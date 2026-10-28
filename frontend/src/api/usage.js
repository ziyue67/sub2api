/**
 * Usage tracking API endpoints
 * Handles usage logs and statistics retrieval
 */
import { apiClient } from './client';
/**
 * List usage logs with optional filters
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @param apiKeyId - Filter by API key ID
 * @returns Paginated list of usage logs
 */
export async function list(page = 1, pageSize = 20, apiKeyId) {
    const params = {
        page,
        page_size: pageSize
    };
    if (apiKeyId !== undefined) {
        params.api_key_id = apiKeyId;
    }
    const { data } = await apiClient.get('/usage', {
        params
    });
    return data;
}
/**
 * Get usage logs with advanced query parameters
 * @param params - Query parameters for filtering and pagination
 * @returns Paginated list of usage logs
 */
export async function query(params, config = {}) {
    const { data } = await apiClient.get('/usage', {
        ...config,
        params
    });
    return data;
}
/**
 * Get usage statistics for a specific period
 * @param period - Time period ('today', 'week', 'month', 'year')
 * @param apiKeyId - Optional API key ID filter
 * @returns Usage statistics
 */
export async function getStats(paramsOrPeriod = 'today', apiKeyId) {
    const params = typeof paramsOrPeriod === 'string'
        ? { period: paramsOrPeriod }
        : { ...paramsOrPeriod };
    if (apiKeyId !== undefined) {
        params.api_key_id = apiKeyId;
    }
    const { data } = await apiClient.get('/usage/stats', {
        params
    });
    return data;
}
/**
 * Get usage statistics for a date range
 * @param startDate - Start date (YYYY-MM-DD format)
 * @param endDate - End date (YYYY-MM-DD format)
 * @param apiKeyId - Optional API key ID filter
 * @returns Usage statistics
 */
export async function getStatsByDateRange(startDate, endDate, apiKeyId) {
    const params = {
        start_date: startDate,
        end_date: endDate
    };
    if (apiKeyId !== undefined) {
        params.api_key_id = apiKeyId;
    }
    const { data } = await apiClient.get('/usage/stats', {
        params
    });
    return data;
}
/**
 * Get usage by date range
 * @param startDate - Start date (YYYY-MM-DD format)
 * @param endDate - End date (YYYY-MM-DD format)
 * @param apiKeyId - Optional API key ID filter
 * @returns Usage logs within date range
 */
export async function getByDateRange(startDate, endDate, apiKeyId) {
    const params = {
        start_date: startDate,
        end_date: endDate,
        page: 1,
        page_size: 100
    };
    if (apiKeyId !== undefined) {
        params.api_key_id = apiKeyId;
    }
    const { data } = await apiClient.get('/usage', {
        params
    });
    return data;
}
/**
 * Get detailed usage log by ID
 * @param id - Usage log ID
 * @returns Usage log details
 */
export async function getById(id) {
    const { data } = await apiClient.get(`/usage/${id}`);
    return data;
}
// ==================== Dashboard API ====================
/**
 * Get user dashboard statistics
 * @returns Dashboard statistics for current user
 */
export async function getDashboardStats() {
    const { data } = await apiClient.get('/usage/dashboard/stats');
    return data;
}
/**
 * Get user usage trend data
 * @param params - Query parameters for filtering
 * @returns Usage trend data for current user
 */
export async function getDashboardTrend(params) {
    const { data } = await apiClient.get('/usage/dashboard/trend', { params });
    return data;
}
/**
 * Get user model usage statistics
 * @param params - Query parameters for filtering
 * @returns Model usage statistics for current user
 */
export async function getDashboardModels(params) {
    const { data } = await apiClient.get('/usage/dashboard/models', { params });
    return data;
}
/**
 * Get daily usage details for one API key owned by the current user.
 * @param apiKeyId - API key ID
 * @param days - Number of days to include (1-90)
 * @returns Daily usage detail rows
 */
export async function getMyApiKeyDailyUsage(apiKeyId, days = 30) {
    const { data } = await apiClient.get(`/user/api-keys/${apiKeyId}/usage/daily`, { params: { days } });
    return data;
}
export async function getDashboardSnapshotV2(params) {
    const { data } = await apiClient.get('/usage/dashboard/snapshot-v2', { params });
    return data;
}
/**
 * Get the Token consumption leaderboard (Top 20, desensitized).
 * Ranking key: total_tokens = input + output + cache + image_output.
 * @param params - days window {1,7,30} and timezone
 * @returns Leaderboard response with Top 20 items
 */
export async function getDashboardLeaderboard(params, options) {
    const { data } = await apiClient.get('/usage/dashboard/leaderboard', {
        params,
        signal: options?.signal
    });
    return data;
}
/**
 * Get batch usage stats for user's own API keys
 * @param apiKeyIds - Array of API key IDs
 * @param options - Optional request options
 * @returns Usage stats map keyed by API key ID
 */
export async function getDashboardApiKeysUsage(apiKeyIds, options) {
    const { data } = await apiClient.post('/usage/dashboard/api-keys-usage', {
        api_key_ids: apiKeyIds
    }, {
        signal: options?.signal
    });
    return data;
}
export async function listMyErrorRequests(params) {
    const { data } = await apiClient.get('/usage/errors', {
        params
    });
    return data;
}
export async function getMyErrorDetail(id) {
    const { data } = await apiClient.get(`/usage/errors/${id}`);
    return data;
}
export const usageAPI = {
    list,
    query,
    getStats,
    getStatsByDateRange,
    getByDateRange,
    getById,
    // Dashboard
    getDashboardStats,
    getDashboardTrend,
    getDashboardModels,
    getMyApiKeyDailyUsage,
    getDashboardSnapshotV2,
    getDashboardApiKeysUsage,
    getDashboardLeaderboard,
    // Error requests
    listMyErrorRequests,
    getMyErrorDetail
};
export default usageAPI;
