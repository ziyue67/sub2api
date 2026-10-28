import { apiClient } from '@/api/client';
const base = '/admin/settings/ip-risk';
export const ipRiskAPI = {
    async get() { return (await apiClient.get(base)).data; },
    async save(config) { return (await apiClient.put(base, config)).data; },
    async events(ip = '', reason = '', category = '', page = 1, pageSize = 20) { return (await apiClient.get(base + '/events', { params: { ip, reason, category, page, page_size: pageSize } })).data; },
    async bans() { return (await apiClient.get(base + '/bans')).data.items; },
    async anonymousBans() { return (await apiClient.get(base + '/anonymous-bans')).data.items; },
    async unbanAnonymous(network) { await apiClient.post(base + '/anonymous-unban', { network }); },
    async unban(network) { await apiClient.post(base + '/unban', { network }); },
    async clearEvents() { await apiClient.delete(base + '/events'); },
    async check(ip, user_agent, auth) { return (await apiClient.post(base + '/check', { ip, user_agent, auth })).data; }
};
