import { apiClient } from '../client';
export async function saveAccountOpsNotificationSettings(settings) {
    return (await apiClient.put('/admin/account-ops/notification-settings', settings)).data;
}
export async function saveAccountOpsRule(id, rule) {
    return (await apiClient.put(`/admin/account-ops/rules/${id}`, rule)).data;
}
export async function saveAccountOpsRulesBatch(accountIds, rule) {
    return (await apiClient.put('/admin/account-ops/rules/batch', { account_ids: accountIds, rule })).data;
}
export async function saveAccountOpsRuleGroups(groups) {
    return (await apiClient.put('/admin/account-ops/rules/batch', { groups })).data;
}
export async function deleteAccountOpsRule(id, metric) {
    return (await apiClient.delete(`/admin/account-ops/rules/${id}`, { params: { metric } })).data;
}
export async function saveAccountOpsWebhook(id, input) {
    return (await apiClient.put(`/admin/account-ops/webhooks/${encodeURIComponent(id)}`, input)).data;
}
export async function deleteAccountOpsWebhook(id) {
    return (await apiClient.delete(`/admin/account-ops/webhooks/${encodeURIComponent(id)}`)).data;
}
export async function getAccountOpsSettings() {
    return (await apiClient.get('/admin/account-ops/config')).data;
}
export async function saveAccountOpsSettings(config) {
    return (await apiClient.put('/admin/account-ops/config', config)).data;
}
export async function getAccountOpsThresholdAccounts() {
    const { data } = await apiClient.get('/admin/account-ops/threshold-accounts');
    return data.items ?? [];
}
export async function getAccountOpsThresholdGroups() {
    const { data } = await apiClient.get('/admin/account-ops/groups');
    return (data.groups ?? []).map(group => ({
        ...group,
        account_ids: Array.isArray(group.account_ids) ? [...group.account_ids] : [],
    }));
}
export async function saveAccountOpsGroups(groups) {
    return (await apiClient.put('/admin/account-ops/groups', { groups })).data;
}
export async function testAccountOpsWebhook(id) {
    return (await apiClient.post(`/admin/account-ops/webhooks/${encodeURIComponent(id)}/test`)).data;
}
export async function getAccountOpsEvents(offset = 0) {
    const { data } = await apiClient.get('/admin/account-ops/alerts', { params: { offset, limit: 50 } });
    return { items: data.items ?? [], has_more: data.has_more ?? false };
}
