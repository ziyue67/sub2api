export const FIRST_TOKEN_THRESHOLDS_MS = {
    warn: 10000,
    slow: 30000,
    critical: 60000,
};
export const DURATION_THRESHOLDS_MS = {
    warn: 60000,
    slow: 180000,
    critical: 300000,
};
const classify = (ms, thresholds) => {
    if (ms >= thresholds.critical)
        return 'critical';
    if (ms >= thresholds.slow)
        return 'slow';
    if (ms >= thresholds.warn)
        return 'warn';
    return 'good';
};
export const firstTokenSeverity = (ms) => classify(ms, FIRST_TOKEN_THRESHOLDS_MS);
export const durationSeverity = (ms) => classify(ms, DURATION_THRESHOLDS_MS);
export const TPS_THRESHOLDS = {
    warn: 20,
    critical: 10,
};
export const tpsSeverity = (tps) => {
    if (tps < TPS_THRESHOLDS.critical)
        return 'critical';
    if (tps < TPS_THRESHOLDS.warn)
        return 'warn';
    return 'good';
};
export const LATENCY_TEXT_CLASSES = {
    good: 'text-emerald-600 dark:text-emerald-400',
    warn: 'text-amber-600 dark:text-amber-400',
    slow: 'text-orange-600 dark:text-orange-400',
    critical: 'text-red-600 dark:text-red-400',
};
/**
 * 渐变色条上端（首字档）；与 VIA/TO 组合成上中下三段渐变，避免硬切割裂感。
 * 某段缺数据（无首字 / 无 TPS）时由调用方沿用总耗时档，全部同档即为纯色。
 */
export const LATENCY_BAR_FROM_CLASSES = {
    good: 'from-emerald-500',
    warn: 'from-amber-400',
    slow: 'from-orange-500',
    critical: 'from-red-500',
};
/** 渐变色条中段（总耗时档）。 */
export const LATENCY_BAR_VIA_CLASSES = {
    good: 'via-emerald-500',
    warn: 'via-amber-400',
    slow: 'via-orange-500',
    critical: 'via-red-500',
};
/** 渐变色条下端（TPS 档）。 */
export const LATENCY_BAR_TO_CLASSES = {
    good: 'to-emerald-500',
    warn: 'to-amber-400',
    slow: 'to-orange-500',
    critical: 'to-red-500',
};
// 与运维 Token 请求统计一致：使用完整耗时，输出可能包含推理 Token。
export const formatUsageOutputRate = (row) => {
    const { output_tokens: outputTokens, duration_ms: durationMs } = row;
    if (row.image_count > 0 || row.image_output_tokens > 0 || row.billing_mode === 'image'
        || (row.request_type && !['sync', 'stream', 'ws_v2', 'cyber'].includes(row.request_type))
        || !Number.isFinite(outputTokens) || outputTokens <= 0
        || durationMs == null || !Number.isFinite(durationMs) || durationMs <= 0) {
        return '—';
    }
    return `${(outputTokens * 1000 / durationMs).toFixed(1)} tok/s`;
};
