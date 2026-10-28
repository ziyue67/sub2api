/**
 * Admin Antigravity API endpoints
 * Handles Antigravity (Google Cloud AI Companion) OAuth flows for administrators
 */
import { apiClient } from '../client';
export async function generateAuthUrl(payload) {
    const { data } = await apiClient.post('/admin/antigravity/oauth/auth-url', payload);
    return data;
}
export async function exchangeCode(payload) {
    const { data } = await apiClient.post('/admin/antigravity/oauth/exchange-code', payload);
    return data;
}
export async function refreshAntigravityToken(refreshToken, proxyId) {
    const payload = { refresh_token: refreshToken };
    if (proxyId)
        payload.proxy_id = proxyId;
    const { data } = await apiClient.post('/admin/antigravity/oauth/refresh-token', payload);
    return data;
}
export default { generateAuthUrl, exchangeCode, refreshAntigravityToken };
