import { apiClient } from '../client';
export function normalizeAstraGateway(value) {
    return {
        account_scheduling: value.account_scheduling ?? false,
        ticket_pool_scheduling: value.ticket_pool_scheduling ?? false,
        scheduling_mode: value.scheduling_mode || 'account',
        scheduling_group_ids: [...(value.scheduling_group_ids || [])],
        cookie_pool: {
            enabled: value.cookie_pool.enabled,
            ip_affinity: value.cookie_pool.ip_affinity ?? false,
            rotate_nodes: value.cookie_pool.rotate_nodes ?? false,
            max_node_attempts: value.cookie_pool.max_node_attempts || 3,
            node_cooldown_seconds: value.cookie_pool.node_cooldown_seconds || 3600,
            ttl_seconds: value.cookie_pool.ttl_seconds || 230,
            source_account_ids: [...(value.cookie_pool.source_account_ids || [])],
            target_account_ids: [...(value.cookie_pool.target_account_ids || [])]
        },
        ws_session: { ttl_seconds: value.ws_session.ttl_seconds || 3600, enabled: value.ws_session.enabled, account_ids: [...(value.ws_session.account_ids || [])] },
        revision: value.revision || ''
    };
}
export async function getAstraGateway() {
    const { data } = await apiClient.get('/admin/settings/astra-routing');
    return normalizeAstraGateway(data);
}
export async function saveAstraGateway(value) {
    const { data } = await apiClient.put('/admin/settings/astra-routing', value);
    return normalizeAstraGateway(data);
}
export async function getAstraGatewayHistory(host = '', passed = true, page = 1) {
    return (await apiClient.get('/admin/accounts/astra-gateway/history', { params: { host, passed, page } })).data;
}
export async function getAstraGatewayRuntime() {
    return (await apiClient.get('/admin/accounts/astra-gateway/status')).data;
}
export async function testAstraGateway(action, account_id = 0, test_kind = 'state_probe') {
    return (await apiClient.post('/admin/accounts/astra-gateway/test', { action, account_id, test_kind }, { timeout: 305000 })).data;
}
export function resolveAstraDependencies(value) {
    const next = normalizeAstraGateway(value);
    if (next.cookie_pool.rotate_nodes)
        next.cookie_pool.ip_affinity = true;
    if (next.ws_session.enabled) {
        next.cookie_pool.enabled = true;
        next.cookie_pool.target_account_ids = [...new Set([...next.cookie_pool.target_account_ids, ...next.ws_session.account_ids.filter(id => !next.cookie_pool.source_account_ids.includes(id))])];
    }
    return next;
}
