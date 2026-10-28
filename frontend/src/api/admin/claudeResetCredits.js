import { apiClient } from '../client';
export async function getClaudeResetCredits(id) {
    const { data } = await apiClient.get(`/admin/accounts/${id}/claude/reset-credits`);
    return data;
}
// Consumes one reset credit (irreversible). The server picks the grant; the key
// identifies one operator confirmation and must be reused when retrying it.
export async function redeemClaudeResetCredit(id, idempotencyKey) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/claude/reset-credits/redeem`, undefined, {
        headers: { 'Idempotency-Key': idempotencyKey }
    });
    return data;
}
