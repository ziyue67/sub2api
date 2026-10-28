import { apiClient } from '../client';
export async function saveTokenGuardV2Runtime(input) {
    return (await apiClient.put('/admin/account-ops/token-guard-v2/runtime', input)).data;
}
export async function updateTokenGuardV2Switches(accountId, input) {
    return (await apiClient.patch(`/admin/account-ops/token-guard-v2/accounts/${accountId}/switches`, input)).data;
}
export async function listTokenGuardV2Accounts() {
    return (await apiClient.get('/admin/account-ops/token-guard-v2/accounts')).data;
}
export async function saveTokenGuardV2Rules(input) {
    return (await apiClient.put('/admin/account-ops/token-guard-v2/rules', input)).data;
}
export async function createTokenGuardV2Account(input) {
    return (await apiClient.post('/admin/account-ops/token-guard-v2/accounts', input)).data;
}
export async function updateTokenGuardV2Account(accountId, input) {
    return (await apiClient.put(`/admin/account-ops/token-guard-v2/accounts/${accountId}`, input)).data;
}
export async function deleteTokenGuardV2Account(accountId) {
    await apiClient.delete(`/admin/account-ops/token-guard-v2/accounts/${accountId}`);
}
export async function probeTokenGuardV2Account(accountId) {
    return (await apiClient.post(`/admin/account-ops/token-guard-v2/accounts/${accountId}/probe`)).data;
}
export async function reloginTokenGuardV2Account(accountId) {
    return (await apiClient.post(`/admin/account-ops/token-guard-v2/accounts/${accountId}/relogin`)).data;
}
export async function resumeTokenGuardV2Budget(accountId) {
    return (await apiClient.post(`/admin/account-ops/token-guard-v2/accounts/${accountId}/resume-relogin-budget`, { confirm: true })).data;
}
