/**
 * User API endpoints
 * Handles user profile management and password changes
 */
import { apiClient } from './client';
import { resolveWeChatOAuthStartStrict, prepareOAuthBindAccessTokenCookie, } from './auth';
/**
 * Get current user profile
 * @returns User profile data
 */
export async function getProfile() {
    const { data } = await apiClient.get('/user/profile');
    return data;
}
/**
 * Update current user profile
 * @param profile - Profile data to update
 * @returns Updated user profile data
 */
export async function updateProfile(profile) {
    const { data } = await apiClient.put('/user', profile);
    return data;
}
/**
 * Change current user password
 * @param passwords - Old and new password
 * @returns Success message
 */
export async function changePassword(oldPassword, newPassword) {
    const payload = {
        old_password: oldPassword,
        new_password: newPassword
    };
    const { data } = await apiClient.put('/user/password', payload);
    return data;
}
/**
 * Send verification code for adding a notify email
 * @param email - Email address to verify
 */
export async function sendNotifyEmailCode(email) {
    await apiClient.post('/user/notify-email/send-code', { email });
}
/**
 * Verify and add a notify email
 * @param email - Email address to add
 * @param code - Verification code
 */
export async function verifyNotifyEmail(email, code) {
    await apiClient.post('/user/notify-email/verify', { email, code });
}
/**
 * Remove a notify email
 * @param email - Email address to remove
 */
export async function removeNotifyEmail(email) {
    await apiClient.delete('/user/notify-email', { data: { email } });
}
/**
 * Toggle a notify email's disabled state
 * @param email - Email address (empty string for primary email placeholder)
 * @param disabled - Whether to disable the email
 */
export async function toggleNotifyEmail(email, disabled) {
    const { data } = await apiClient.put('/user/notify-email/toggle', { email, disabled });
    return data;
}
export async function sendEmailBindingCode(email) {
    await apiClient.post('/user/account-bindings/email/send-code', { email });
}
export async function bindEmailIdentity(payload) {
    const { data } = await apiClient.post('/user/account-bindings/email', payload);
    return data;
}
export async function unbindAuthIdentity(provider) {
    const { data } = await apiClient.delete(`/user/account-bindings/${provider}`);
    return data;
}
export function resolveWeChatOAuthMode() {
    if (typeof navigator === 'undefined') {
        return 'open';
    }
    return /MicroMessenger/i.test(navigator.userAgent) ? 'mp' : 'open';
}
function resolveWeChatOAuthBindingMode(settings) {
    if (settings) {
        return resolveWeChatOAuthStartStrict(settings).mode;
    }
    return resolveWeChatOAuthMode();
}
export function buildOAuthBindingStartURL(provider, options = {}) {
    const redirectTo = options.redirectTo?.trim() || '/profile';
    const apiBase = import.meta.env.VITE_API_BASE_URL || '/api/v1';
    const normalized = apiBase.replace(/\/$/, '');
    const params = new URLSearchParams({
        redirect: redirectTo,
        intent: 'bind_current_user'
    });
    if (provider === 'wechat') {
        const mode = resolveWeChatOAuthBindingMode(options.wechatOAuthSettings);
        if (!mode) {
            return null;
        }
        params.set('mode', mode);
    }
    return `${normalized}/auth/oauth/${provider}/bind/start?${params.toString()}`;
}
export async function startOAuthBinding(provider, options = {}) {
    if (typeof window === 'undefined') {
        return;
    }
    const startURL = buildOAuthBindingStartURL(provider, options);
    if (!startURL) {
        return;
    }
    await prepareOAuthBindAccessTokenCookie();
    window.location.href = startURL;
}
export async function getAffiliateDetail() {
    const { data } = await apiClient.get('/user/aff');
    return data;
}
export async function transferAffiliateQuota() {
    const { data } = await apiClient.post('/user/aff/transfer');
    return data;
}
/**
 * 获取当前用户的平台限额 + 用量。
 */
export async function getMyPlatformQuotas() {
    const { data } = await apiClient.get('/user/platform-quotas');
    return data;
}
export const userAPI = {
    getProfile,
    updateProfile,
    changePassword,
    sendNotifyEmailCode,
    verifyNotifyEmail,
    removeNotifyEmail,
    toggleNotifyEmail,
    sendEmailBindingCode,
    bindEmailIdentity,
    unbindAuthIdentity,
    buildOAuthBindingStartURL,
    startOAuthBinding,
    getAffiliateDetail,
    transferAffiliateQuota,
    getMyPlatformQuotas,
};
export default userAPI;
