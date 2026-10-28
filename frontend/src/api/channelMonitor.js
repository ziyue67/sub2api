/**
 * User-facing Channel Monitor API endpoints
 * Read-only views for end users to inspect channel availability/status.
 */
import { apiClient } from './client';
/**
 * List all monitor views available to the current user.
 */
export async function list(options) {
    const { data } = await apiClient.get('/channel-monitors', {
        signal: options?.signal,
    });
    return data;
}
/**
 * Get detailed status (multi-window availability + latency) for a single monitor.
 */
export async function status(id) {
    const { data } = await apiClient.get(`/channel-monitors/${id}/status`);
    return data;
}
export const channelMonitorUserAPI = {
    list,
    status,
};
export default channelMonitorUserAPI;
