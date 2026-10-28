/**
 * Admin User Attributes API endpoints
 * Handles user custom attribute definitions and values
 */
import { apiClient } from '../client';
/**
 * Get all attribute definitions
 */
export async function listDefinitions() {
    const { data } = await apiClient.get('/admin/user-attributes');
    return data;
}
/**
 * Get enabled attribute definitions only
 */
export async function listEnabledDefinitions() {
    const { data } = await apiClient.get('/admin/user-attributes', {
        params: { enabled: true }
    });
    return data;
}
/**
 * Create a new attribute definition
 */
export async function createDefinition(request) {
    const { data } = await apiClient.post('/admin/user-attributes', request);
    return data;
}
/**
 * Update an attribute definition
 */
export async function updateDefinition(id, request) {
    const { data } = await apiClient.put(`/admin/user-attributes/${id}`, request);
    return data;
}
/**
 * Delete an attribute definition
 */
export async function deleteDefinition(id) {
    const { data } = await apiClient.delete(`/admin/user-attributes/${id}`);
    return data;
}
/**
 * Reorder attribute definitions
 */
export async function reorderDefinitions(ids) {
    const { data } = await apiClient.put('/admin/user-attributes/reorder', {
        ids
    });
    return data;
}
/**
 * Get user's attribute values
 */
export async function getUserAttributeValues(userId) {
    const { data } = await apiClient.get(`/admin/users/${userId}/attributes`);
    return data;
}
/**
 * Update user's attribute values (batch)
 */
export async function updateUserAttributeValues(userId, values) {
    const { data } = await apiClient.put(`/admin/users/${userId}/attributes`, { values });
    return data;
}
/**
 * Get attribute values for multiple users
 */
export async function getBatchUserAttributes(userIds) {
    const { data } = await apiClient.post('/admin/user-attributes/batch', { user_ids: userIds });
    return data;
}
export const userAttributesAPI = {
    listDefinitions,
    listEnabledDefinitions,
    createDefinition,
    updateDefinition,
    deleteDefinition,
    reorderDefinitions,
    getUserAttributeValues,
    updateUserAttributeValues,
    getBatchUserAttributes
};
export default userAttributesAPI;
