import { apiClient } from '../client';
// Unlike the settings parser, imports must reject malformed lines rather than
// silently dropping an account from a batch.
export function parseTwoFALoginText(raw) {
    const lines = raw.split(/\r?\n/).map(line => line.trim()).filter(Boolean);
    if (!lines.length || lines.length > 100)
        throw new Error('invalid_batch');
    const seen = new Set();
    return lines.map(line => {
        const parts = line.includes('----') ? line.split('----') : line.split(',');
        const [email = '', password = '', mfa = ''] = parts.map(part => part.trim());
        const normalizedEmail = email.toLowerCase();
        if (parts.length !== 3 || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email) ||
            !password || !mfa || seen.has(normalizedEmail))
            throw new Error('invalid_batch');
        seen.add(normalizedEmail);
        return { email: normalizedEmail, password, mfa_secret: mfa };
    });
}
const twoFALoginPath = '/admin/account-ops/token-guard/two-fa-login';
export async function startTwoFALogin(entry) {
    return (await apiClient.post(twoFALoginPath, { ...entry, credential_target: 'operations' })).data;
}
export async function getTwoFALogin(id) {
    return (await apiClient.get(`${twoFALoginPath}/${id}`)).data;
}
export async function deleteTwoFALogin(id) {
    await apiClient.delete(`${twoFALoginPath}/${id}`);
}
export function parseTokenGuardReloginText(raw) {
    return raw
        .split(/\r?\n/)
        .map(line => line.trim())
        .filter(Boolean)
        .map(line => {
        const [email = '', password = '', mfa = ''] = line.includes('----')
            ? line.split('----', 3)
            : line.split(',');
        return { email: email.trim(), password: password.trim(), mfa_secret: mfa.trim() };
    })
        .filter(item => item.email && item.password);
}
export function formatTokenGuardReloginText(accounts) {
    return (accounts ?? [])
        .map(item => `${item.email}----${item.password}----${item.mfa_secret}`)
        .join('\n');
}
export async function getTokenGuardStatus() {
    return (await apiClient.get('/admin/account-ops/token-guard/status')).data;
}
export async function saveTokenGuardConfig(config) {
    return (await apiClient.put('/admin/account-ops/token-guard/config', config)).data;
}
export async function runTokenGuard() {
    return (await apiClient.post('/admin/account-ops/token-guard/run')).data;
}
export async function startTokenGuardRun() {
    return (await apiClient.post('/admin/account-ops/token-guard/run/start')).data;
}
export async function getTokenGuardJob(jobId) {
    return (await apiClient.get(`/admin/account-ops/token-guard/jobs/${jobId}`)).data;
}
export async function cancelTokenGuardJob(jobId) {
    return (await apiClient.post(`/admin/account-ops/token-guard/jobs/${jobId}/cancel`)).data;
}
export async function reloginTokenGuardAccount(accountId) {
    return (await apiClient.post(`/admin/account-ops/token-guard/accounts/${accountId}/relogin`)).data;
}
