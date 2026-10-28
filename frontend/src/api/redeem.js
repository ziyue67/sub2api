/**
 * Redeem code API endpoints
 * Handles redeem code redemption for users
 */
import { apiClient } from './client';
/**
 * Redeem a code
 * @param code - Redeem code string
 * @returns Redemption result with updated balance or concurrency
 */
export async function redeem(code) {
    const payload = { code };
    const { data } = await apiClient.post('/redeem', payload);
    return data;
}
/**
 * Get user's redemption history
 * @returns The requested page of redeemed codes and the total count
 */
export async function getHistory(page = 1, pageSize = 20) {
    const { data } = await apiClient.get('/redeem/history', {
        params: { page, page_size: pageSize }
    });
    return data;
}
export const redeemAPI = {
    redeem,
    getHistory
};
export default redeemAPI;
