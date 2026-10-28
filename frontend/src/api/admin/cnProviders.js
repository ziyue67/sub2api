/**
 * Admin CN providers (Kimi / Zhipu / DeepSeek) API endpoints.
 * Coding-plan rolling-window quota probe + payg balance probe.
 */
import { apiClient } from '../client';
/** 查询 Coding Plan 滚动窗口用量（5h + weekly）。 */
export async function queryQuota(id) {
    const { data } = await apiClient.get(`/admin/cn-providers/accounts/${id}/quota`);
    return data;
}
/** 查询 payg 账号余额。 */
export async function queryBalance(id) {
    const { data } = await apiClient.get(`/admin/cn-providers/accounts/${id}/balance`);
    return data;
}
export default {
    queryQuota,
    queryBalance
};
