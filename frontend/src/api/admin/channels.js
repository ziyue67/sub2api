/**
 * Admin Channels API endpoints
 * Handles channel management for administrators
 */
import { apiClient } from '../client';
/**
 * List channels with pagination
 */
export async function list(page = 1, pageSize = 20, filters, options) {
    const { data } = await apiClient.get('/admin/channels', {
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
 * Get channel by ID
 */
export async function getById(id) {
    const { data } = await apiClient.get(`/admin/channels/${id}`);
    return data;
}
/**
 * Create a new channel
 */
export async function create(req) {
    const { data } = await apiClient.post('/admin/channels', req);
    return data;
}
/**
 * Update a channel
 */
export async function update(id, req) {
    const { data } = await apiClient.put(`/admin/channels/${id}`, req);
    return data;
}
/**
 * Delete a channel
 */
export async function remove(id) {
    await apiClient.delete(`/admin/channels/${id}`);
}
export async function getModelDefaultPricing(model) {
    const { data } = await apiClient.get('/admin/channels/model-pricing', {
        params: { model }
    });
    return data;
}
/**
 * Fetch the latest model names from the LiteLLM pricing catalog for the given platform
 */
export async function syncPricingModels(platform) {
    const { data } = await apiClient.get('/admin/channels/pricing/sync-models', {
        params: { platform }
    });
    return data;
}
const channelsAPI = { list, getById, create, update, remove, getModelDefaultPricing, syncPricingModels };
export default channelsAPI;
