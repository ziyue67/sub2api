/**
 * TOTP (2FA) API endpoints
 * Handles Two-Factor Authentication with Google Authenticator
 */
import { apiClient } from './client';
/**
 * Get TOTP status for current user
 * @returns TOTP status including enabled state and feature availability
 */
export async function getStatus() {
    const { data } = await apiClient.get('/user/totp/status');
    return data;
}
/**
 * Get verification method for TOTP operations
 * @returns Method ('email' or 'password') required for setup/disable
 */
export async function getVerificationMethod() {
    const { data } = await apiClient.get('/user/totp/verification-method');
    return data;
}
/**
 * Send email verification code for TOTP operations
 * @returns Success response
 */
export async function sendVerifyCode() {
    const { data } = await apiClient.post('/user/totp/send-code');
    return data;
}
/**
 * Initiate TOTP setup - generates secret and QR code
 * @param request - Email code or password depending on verification method
 * @returns Setup response with secret, QR code URL, and setup token
 */
export async function initiateSetup(request) {
    const { data } = await apiClient.post('/user/totp/setup', request || {});
    return data;
}
/**
 * Complete TOTP setup by verifying the code
 * @param request - TOTP code and setup token
 * @returns Enable response with success status and enabled timestamp
 */
export async function enable(request) {
    const { data } = await apiClient.post('/user/totp/enable', request);
    return data;
}
/**
 * Disable TOTP for current user
 * @param request - Email code or password depending on verification method
 * @returns Success response
 */
export async function disable(request) {
    const { data } = await apiClient.post('/user/totp/disable', request);
    return data;
}
/**
 * Verify a TOTP code to grant the current session a short-lived step-up
 * (sudo) window for sensitive operations (account export, DB backup download...).
 * @param code - 6-digit TOTP code
 */
export async function stepUp(code) {
    const { data } = await apiClient.post('/user/totp/step-up', { code });
    return data;
}
export const totpAPI = {
    getStatus,
    getVerificationMethod,
    sendVerifyCode,
    initiateSetup,
    enable,
    disable,
    stepUp
};
export default totpAPI;
