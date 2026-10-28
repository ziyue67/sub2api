import { apiClient } from '../client';
const path = '/admin/account-ops/token-guard-v2/encryption';
export async function getCredentialEncryption() {
    return (await apiClient.get(path)).data;
}
export async function initializeCredentialEncryption() {
    return (await apiClient.post(`${path}/initialize`)).data;
}
