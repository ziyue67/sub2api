/**
 * Model Plaza API（公开端点，可匿名访问）
 * 以分组为中心的模型价目：分组信息 + 模型渠道定价 + LiteLLM 官方参考价。
 * 带 token 请求时后端会额外返回专属分组与用户专属倍率。
 */
import { apiClient } from './client';
/** 获取模型广场数据。开关未启用时后端返回 404。 */
export async function getModelPlaza(options) {
    const { data } = await apiClient.get('/model-plaza', {
        signal: options?.signal
    });
    return data;
}
export const modelPlazaAPI = { getModelPlaza };
export default modelPlazaAPI;
