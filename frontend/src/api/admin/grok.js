/**
 * Admin Grok/xAI API endpoints
 * Handles xAI OAuth flows for administrators.
 */
import { apiClient } from '../client';
const GROK_AUTHORIZATION_TIMEOUT_MS = 120000;
export async function getCapabilities() {
    const { data } = await apiClient.get('/admin/grok/oauth/capabilities');
    return data;
}
const GROK_SSO_IMPORT_CONCURRENCY = 3;
const GROK_SSO_IMPORT_TIMEOUT_PER_BATCH_MS = 90000;
const GROK_SSO_IMPORT_TIMEOUT_BUFFER_MS = 90000;
export function getGrokSSOImportTimeout(keyCount) {
    const batches = Math.ceil(Math.max(1, keyCount) / GROK_SSO_IMPORT_CONCURRENCY);
    return batches * GROK_SSO_IMPORT_TIMEOUT_PER_BATCH_MS + GROK_SSO_IMPORT_TIMEOUT_BUFFER_MS;
}
export async function generateAuthUrl(payload) {
    const { data } = await apiClient.post('/admin/grok/oauth/auth-url', payload);
    return data;
}
export async function exchangeCode(payload) {
    const { data } = await apiClient.post('/admin/grok/oauth/exchange-code', payload);
    return data;
}
export async function refreshGrokToken(refreshToken, proxyId) {
    const payload = { refresh_token: refreshToken };
    if (proxyId)
        payload.proxy_id = proxyId;
    const { data } = await apiClient.post('/admin/grok/oauth/refresh-token', payload);
    return data;
}
export async function queryQuota(id) {
    const { data } = await apiClient.get(`/admin/grok/accounts/${id}/quota`);
    return data;
}
export async function resetQuota(id) {
    const { data } = await apiClient.post(`/admin/grok/accounts/${id}/reset-quota`);
    return data;
}
export async function createFromSSO(payload) {
    const { data } = await apiClient.post('/admin/grok/sso-to-oauth', payload, { timeout: getGrokSSOImportTimeout(payload.sso_tokens.length) });
    return data;
}
/** Validate a browser SSO cookie and convert to Build OAuth tokens (no raw SSO stored). */
export async function validateSSOToken(ssoToken, proxyId) {
    const payload = { sso_token: ssoToken };
    if (proxyId)
        payload.proxy_id = proxyId;
    const { data } = await apiClient.post('/admin/grok/oauth/sso-token', payload, {
        timeout: GROK_AUTHORIZATION_TIMEOUT_MS
    });
    return data;
}
/**
 * Password login → ephemeral SSO → Build OAuth.
 * Password is only sent over the wire for this call; never persist it in credentials.
 */
export async function authorizePassword(emailAndPassword, proxyId) {
    // Format: email----password (password may contain dashes).
    const sep = '----';
    const idx = emailAndPassword.indexOf(sep);
    const email = (idx >= 0 ? emailAndPassword.slice(0, idx) : emailAndPassword).trim();
    const password = idx >= 0 ? emailAndPassword.slice(idx + sep.length) : '';
    const payload = { email, password };
    if (proxyId)
        payload.proxy_id = proxyId;
    const { data } = await apiClient.post('/admin/grok/oauth/password', payload, {
        timeout: GROK_AUTHORIZATION_TIMEOUT_MS
    });
    return data;
}
export default {
    generateAuthUrl,
    getCapabilities,
    exchangeCode,
    refreshGrokToken,
    queryQuota,
    resetQuota,
    createFromSSO,
    validateSSOToken,
    authorizePassword,
};
