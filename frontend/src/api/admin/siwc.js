import { apiClient } from '../client';
const path = (id) => `/admin/accounts/${id}/siwc`;
export async function getSIWCStatus(id) {
    return (await apiClient.get(path(id))).data;
}
export async function authorizeSIWC(id) {
    return (await apiClient.post(`${path(id)}/authorize`)).data;
}
export async function completeSIWC(id, sessionId, callbackUrl) {
    await apiClient.post(`${path(id)}/callback`, { session_id: sessionId, callback_url: callbackUrl });
}
export async function loginSIWCWithSavedCredentials(id) {
    await apiClient.post(`/admin/accounts/${id}/openai-reauth`, { oauth_profile: 'siwc' });
}
