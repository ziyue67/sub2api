/**
 * Admin Redeem Codes API endpoints
 * Handles redeem code generation and management for administrators
 */
import { apiClient } from '../client';
/**
 * List all redeem codes with pagination
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @param filters - Optional filters
 * @returns Paginated list of redeem codes
 */
export async function list(page = 1, pageSize = 20, filters, options) {
    const { data } = await apiClient.get('/admin/redeem-codes', {
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
 * Get redeem code by ID
 * @param id - Redeem code ID
 * @returns Redeem code details
 */
export async function getById(id) {
    const { data } = await apiClient.get(`/admin/redeem-codes/${id}`);
    return data;
}
/**
 * Generate new redeem codes
 * @param count - Number of codes to generate
 * @param type - Type of redeem code
 * @param value - Value of the code
 * @param groupId - Group ID (required for subscription type)
 * @param validityDays - Validity days (for subscription type)
 * @param expiresInDays - Days before the code itself expires
 * @returns Array of generated redeem codes
 */
export async function generate(count, type, value, groupId, validityDays, expiresInDays) {
    const payload = {
        count,
        type,
        value
    };
    // 订阅类型专用字段
    if (type === 'subscription') {
        payload.group_id = groupId;
        if (validityDays && validityDays > 0) {
            payload.validity_days = validityDays;
        }
    }
    if (expiresInDays && expiresInDays > 0) {
        payload.expires_in_days = expiresInDays;
    }
    const { data } = await apiClient.post('/admin/redeem-codes/generate', payload);
    return data;
}
/**
 * Delete redeem code
 * @param id - Redeem code ID
 * @returns Success confirmation
 */
export async function deleteCode(id) {
    const { data } = await apiClient.delete(`/admin/redeem-codes/${id}`);
    return data;
}
/**
 * Batch delete redeem codes
 * @param ids - Array of redeem code IDs
 * @returns Success confirmation
 */
export async function batchDelete(ids) {
    const { data } = await apiClient.post('/admin/redeem-codes/batch-delete', { ids });
    return data;
}
/**
 * Batch update selected redeem code fields
 * @param ids - Array of redeem code IDs
 * @param fields - Field collection to update
 * @returns Updated count
 */
export async function batchUpdate(ids, fields) {
    const { data } = await apiClient.post('/admin/redeem-codes/batch-update', { ids, fields });
    return data;
}
/**
 * Expire redeem code
 * @param id - Redeem code ID
 * @returns Updated redeem code
 */
export async function expire(id) {
    const { data } = await apiClient.post(`/admin/redeem-codes/${id}/expire`);
    return data;
}
/**
 * Get redeem code statistics
 * @returns Statistics about redeem codes
 */
export async function getStats() {
    const { data } = await apiClient.get('/admin/redeem-codes/stats');
    return data;
}
/**
 * Export redeem codes to CSV
 * @param filters - Optional filters
 * @returns CSV data as blob
 */
export async function exportCodes(filters) {
    const response = await apiClient.get('/admin/redeem-codes/export', {
        params: filters,
        responseType: 'blob'
    });
    return response.data;
}
export const redeemAPI = {
    list,
    getById,
    generate,
    delete: deleteCode,
    batchDelete,
    batchUpdate,
    expire,
    getStats,
    exportCodes
};
export default redeemAPI;
