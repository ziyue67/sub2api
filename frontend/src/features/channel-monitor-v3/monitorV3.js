/** Cell and dot colors sampled to match the status-page reference. */
export const MONITOR_V3_COLORS = {
    operational: 'bg-[#22c39b]',
    degraded: 'bg-[#f6b928]',
    down: 'bg-[#f2715a]',
    unknown: 'bg-gray-300 dark:bg-dark-500',
    insufficient: 'bg-gray-300 dark:bg-dark-500',
};
export const MONITOR_V3_EMPTY_CELL = 'bg-gray-200 dark:bg-dark-600';
/** 97.4612 → "97.46%", 88.597 → "88.6%", null → "—". */
export function formatMonitorV3Availability(value) {
    if (value == null || !Number.isFinite(value))
        return '—';
    const clamped = Math.min(100, Math.max(0, value));
    let rounded = Math.round(clamped * 100) / 100;
    // One failed check must never read as 100%.
    if (rounded >= 100 && clamped < 100)
        rounded = 99.99;
    return `${Number(rounded.toFixed(2))}%`;
}
/** 0.2 → "×0.2", 1.25 → "×1.25"; undefined/null hides the badge. */
export function formatMonitorV3Multiplier(value) {
    if (value == null || !Number.isFinite(value))
        return '';
    return `×${Number(value.toFixed(4))}`;
}
function pad(value) {
    return String(value).padStart(2, '0');
}
/** "10/4 11:53" in the viewer's local time. */
export function formatMonitorV3Short(value) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime()))
        return '—';
    return `${date.getMonth() + 1}/${date.getDate()} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
/** "2026/9/24 15:31:55" in the viewer's local time. */
export function formatMonitorV3Full(value) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime()))
        return '—';
    return `${date.getFullYear()}/${date.getMonth() + 1}/${date.getDate()} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}
/**
 * Names the viewer's time zone the way the page states it: "北京时间" style
 * names for UTC+8, otherwise a plain UTC offset such as "UTC-7".
 */
export function monitorV3TimezoneOffsetHours(date = new Date()) {
    return -date.getTimezoneOffset() / 60;
}
export function formatMonitorV3Offset(hours) {
    if (hours === 0)
        return 'UTC';
    const sign = hours > 0 ? '+' : '-';
    const abs = Math.abs(hours);
    const whole = Math.floor(abs);
    const minutes = Math.round((abs - whole) * 60);
    return `UTC${sign}${whole}${minutes ? ':' + pad(minutes) : ''}`;
}
/** The window label shows the slot range actually covered, ending no later than now. */
export function monitorV3WindowRange(start, end, generatedAt) {
    const endTime = Math.min(new Date(end).getTime(), new Date(generatedAt).getTime());
    return { from: formatMonitorV3Short(start), to: formatMonitorV3Short(endTime) };
}
/** Two columns; a category left alone on the last row spans both. */
export function monitorV3CategoryLayout(categories) {
    return categories.map((category, index) => ({
        category,
        fullWidth: index === categories.length - 1 && categories.length % 2 === 1,
    }));
}
/** 0.98234 → "98.23%". */
export function formatMonitorV3Rate(rate) {
    if (rate == null || !Number.isFinite(rate))
        return '—';
    return formatMonitorV3Availability(rate * 100);
}
/** A first-token P50 is a histogram bucket: 2000 → "≤ 2s", 500 → "≤ 0.5s". */
export function formatMonitorV3Bucket(ms) {
    if (ms == null || !Number.isFinite(ms))
        return '—';
    if (ms >= 2147483647)
        return '> 600s';
    return `≤ ${Number((ms / 1000).toFixed(2))}s`;
}
/** A probe time in seconds: 1834 → "1.8", 12000 → "12", 40 → "<0.1". */
export function formatMonitorV3Seconds(ms) {
    if (ms < 100)
        return '<0.1';
    return String(Number((ms / 1000).toFixed(ms < 10000 ? 1 : 0)));
}
/** "10/4 15:35 - 15:40", or both dates when the slot crosses midnight. */
export function formatMonitorV3Slot(start, intervalMinutes) {
    const from = new Date(start);
    if (Number.isNaN(from.getTime()))
        return '—';
    const to = new Date(from.getTime() + intervalMinutes * 60000);
    const sameDay = from.getDate() === to.getDate() && from.getMonth() === to.getMonth();
    const end = sameDay ? `${pad(to.getHours())}:${pad(to.getMinutes())}` : formatMonitorV3Short(to);
    return `${formatMonitorV3Short(from)} - ${end}`;
}
