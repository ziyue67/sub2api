/**
 * 高峰时段倍率的共享展示逻辑。
 *
 * 高峰窗口由后端按服务器全局时区判定（Group.PeakMultiplierAt），
 * 前端展示必须带上服务器时区标注（来自公共设置 server_utc_offset），
 * 避免用户按浏览器本地时间误读计费窗口。
 */
export function hasPeakRate(fields) {
    return Boolean(fields?.peak_rate_enabled && fields.peak_start && fields.peak_end);
}
/** "+08:00" → "UTC+08:00"；旧缓存无该字段时返回空串，调用方降级为不带时区标注 */
export function serverTimezoneLabel(utcOffset) {
    return utcOffset ? `UTC${utcOffset}` : '';
}
/** "14:00-18:00 ×2 (UTC+08:00)"，tzLabel 为空时省略括号部分 */
export function formatPeakRateWindow(fields, tzLabel) {
    if (!hasPeakRate(fields) || !fields)
        return '';
    const base = `${fields.peak_start}-${fields.peak_end} ×${fields.peak_rate_multiplier ?? 1}`;
    return tzLabel ? `${base} (${tzLabel})` : base;
}
