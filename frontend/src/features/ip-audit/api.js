import { apiClient } from '@/api/client';
export async function listIPAudit(params, signal) { return (await apiClient.get('/admin/settings/ip-audit', { params, signal })).data; }
export async function releaseIPAudit(entry, reason) {
    return (await apiClient.post('/admin/settings/ip-audit/release', { source: entry.source, id: entry.id, network: entry.network, check_ip: entry.check_ip, version: entry.version, user_id: entry.user_id, confirm: true, reason })).data;
}
export const auditTab = (value) => value === 'ip' || value === 'config' ? value : 'events';
