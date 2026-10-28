/**
 * Pelican showcase API: the user gallery of scheduled Pelican HTML results.
 * Snapshots never carry account identity — only the group, model and timing.
 */
import { apiClient } from './client';
export async function getShowcase(options) {
    const { data } = await apiClient.get('/pelican-showcase', { signal: options?.signal });
    return data;
}
export async function getShowcaseItem(id) {
    const { data } = await apiClient.get(`/pelican-showcase/items/${id}`);
    return data;
}
/** Admin only: take one snapshot off the gallery. */
export async function removeShowcaseItem(id) {
    await apiClient.delete(`/admin/pelican-showcase/items/${id}`);
}
export const pelicanShowcaseAPI = { getShowcase, getShowcaseItem, removeShowcaseItem };
export default pelicanShowcaseAPI;
