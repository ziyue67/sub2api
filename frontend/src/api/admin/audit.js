/**
 * Admin operation audit log API.
 *
 * The audit log is admin-only (not exposed to end users). It records
 * management-plane operations with masked header credentials and redacted
 * request bodies. Entries cannot be deleted individually; the whole log can
 * only be cleared with a fresh TOTP verification.
 */
import { apiClient } from '../client';
/**
 * List audit logs (paginated, filterable).
 */
export async function list(params) {
    const { data } = await apiClient.get('/admin/audit-logs', { params });
    return data;
}
/**
 * Get a single audit log entry (includes the redacted request body).
 */
export async function get(id) {
    const { data } = await apiClient.get(`/admin/audit-logs/${id}`);
    return data;
}
/**
 * Clear all audit logs. Requires a fresh TOTP code (verified server-side);
 * unavailable when 2FA is not enabled for the operator.
 * @param totpCode - current 6-digit TOTP code
 */
export async function clear(totpCode) {
    const { data } = await apiClient.post('/admin/audit-logs/clear', { totp_code: totpCode });
    return data;
}
export const auditAPI = {
    list,
    get,
    clear
};
export default auditAPI;
