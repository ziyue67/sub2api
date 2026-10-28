export function sanitizeUrl(value, options = {}) {
    const trimmed = value.trim();
    if (!trimmed) {
        return '';
    }
    if (options.allowRelative && trimmed.startsWith('/') && !trimmed.startsWith('//')) {
        return trimmed;
    }
    // 允许 data:image/ 开头的 data URL（仅限图片类型）
    if (options.allowDataUrl && trimmed.startsWith('data:image/')) {
        return trimmed;
    }
    // 只接受绝对 URL，不使用 base URL 来避免相对路径被解析为当前域名
    // 检查是否以 http:// 或 https:// 开头
    if (!trimmed.match(/^https?:\/\//i)) {
        return '';
    }
    try {
        const parsed = new URL(trimmed);
        const protocol = parsed.protocol.toLowerCase();
        if (protocol !== 'http:' && protocol !== 'https:') {
            return '';
        }
        return parsed.toString();
    }
    catch {
        return '';
    }
}
