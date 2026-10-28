/**
 * User Channels API endpoints (non-admin)
 * 用户侧「可用渠道」聚合查询：渠道 + 用户可访问的分组 + 支持模型（含定价）。
 */
import { apiClient } from './client';
/** 列出当前用户可见的「可用渠道」（与 /groups/available 保持一致，返回平数组）。 */
export async function getAvailable(options) {
    const { data } = await apiClient.get('/channels/available', {
        signal: options?.signal
    });
    return data;
}
export const userChannelsAPI = { getAvailable };
export default userChannelsAPI;
