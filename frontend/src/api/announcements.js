/**
 * User Announcements API endpoints
 */
import { apiClient } from './client';
export async function list(unreadOnly = false) {
    const { data } = await apiClient.get('/announcements', {
        params: unreadOnly ? { unread_only: 1 } : {}
    });
    return data;
}
export async function markRead(id) {
    const { data } = await apiClient.post(`/announcements/${id}/read`);
    return data;
}
const announcementsAPI = {
    list,
    markRead
};
export default announcementsAPI;
