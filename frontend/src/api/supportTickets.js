/**
 * Support tickets ("网站工单") — user and admin endpoints.
 * Every call answers SUPPORT_TICKET_DISABLED (403) while the feature switch is off.
 */
import { apiClient } from './client';
export const SUPPORT_TICKET_TITLE_MAX = 100;
export const SUPPORT_TICKET_BODY_MAX = 5000;
export const SUPPORT_TICKET_CATEGORY_MAX = 32;
export const SUPPORT_TICKET_MAX_CATEGORIES = 20;
export const SUPPORT_TICKET_NOTICE_MAX = 500;
export const SUPPORT_TICKET_MAX_OPEN_LIMIT = 50;
/** Length as the server counts it: Unicode code points, not UTF-16 units. */
export function supportTicketLength(text) {
    return [...text].length;
}
// ---- users ----
export async function getMySummary() {
    const { data } = await apiClient.get('/support-tickets/summary');
    return data;
}
export async function listMyTickets(params) {
    const { data } = await apiClient.get('/support-tickets', { params });
    return data;
}
export async function createTicket(request) {
    const { data } = await apiClient.post('/support-tickets', request);
    return data;
}
export async function getMyTicket(id) {
    const { data } = await apiClient.get(`/support-tickets/${id}`);
    return data;
}
export async function replyMyTicket(id, body) {
    const { data } = await apiClient.post(`/support-tickets/${id}/messages`, { body });
    return data;
}
export async function closeMyTicket(id) {
    const { data } = await apiClient.post(`/support-tickets/${id}/close`);
    return data;
}
export async function reopenMyTicket(id) {
    const { data } = await apiClient.post(`/support-tickets/${id}/reopen`);
    return data;
}
// ---- admins ----
export async function getAdminSummary() {
    const { data } = await apiClient.get('/admin/support-tickets/summary');
    return data;
}
export async function listTickets(params) {
    const { data } = await apiClient.get('/admin/support-tickets', { params });
    return data;
}
export async function getTicket(id) {
    const { data } = await apiClient.get(`/admin/support-tickets/${id}`);
    return data;
}
export async function replyTicket(id, body, status) {
    const { data } = await apiClient.post(`/admin/support-tickets/${id}/messages`, { body, status });
    return data;
}
/** processing, closed, or pending to reopen a closed ticket. */
export async function setTicketStatus(id, status) {
    const { data } = await apiClient.post(`/admin/support-tickets/${id}/status`, { status });
    return data;
}
export async function deleteTicket(id) {
    await apiClient.delete(`/admin/support-tickets/${id}`);
}
