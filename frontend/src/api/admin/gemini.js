/**
 * Admin Gemini API endpoints
 * Handles Gemini OAuth flows for administrators
 */
import { apiClient } from '../client';
export async function generateAuthUrl(payload) {
    const { data } = await apiClient.post('/admin/gemini/oauth/auth-url', payload);
    return data;
}
export async function exchangeCode(payload) {
    const { data } = await apiClient.post('/admin/gemini/oauth/exchange-code', payload);
    return data;
}
export async function getCapabilities() {
    const { data } = await apiClient.get('/admin/gemini/oauth/capabilities');
    return data;
}
export default { generateAuthUrl, exchangeCode, getCapabilities };
