/**
 * Admin Error Passthrough Rules API endpoints
 * Handles error passthrough rule management for administrators
 */
import { apiClient } from '../client';
/**
 * List all error passthrough rules
 * @returns List of all rules sorted by priority
 */
export async function list() {
    const { data } = await apiClient.get('/admin/error-passthrough-rules');
    return data;
}
/**
 * Get rule by ID
 * @param id - Rule ID
 * @returns Rule details
 */
export async function getById(id) {
    const { data } = await apiClient.get(`/admin/error-passthrough-rules/${id}`);
    return data;
}
/**
 * Create new rule
 * @param ruleData - Rule data
 * @returns Created rule
 */
export async function create(ruleData) {
    const { data } = await apiClient.post('/admin/error-passthrough-rules', ruleData);
    return data;
}
/**
 * Update rule
 * @param id - Rule ID
 * @param updates - Fields to update
 * @returns Updated rule
 */
export async function update(id, updates) {
    const { data } = await apiClient.put(`/admin/error-passthrough-rules/${id}`, updates);
    return data;
}
/**
 * Delete rule
 * @param id - Rule ID
 * @returns Success confirmation
 */
export async function deleteRule(id) {
    const { data } = await apiClient.delete(`/admin/error-passthrough-rules/${id}`);
    return data;
}
/**
 * Toggle rule enabled status
 * @param id - Rule ID
 * @param enabled - New enabled status
 * @returns Updated rule
 */
export async function toggleEnabled(id, enabled) {
    return update(id, { enabled });
}
export const errorPassthroughAPI = {
    list,
    getById,
    create,
    update,
    delete: deleteRule,
    toggleEnabled
};
export default errorPassthroughAPI;
