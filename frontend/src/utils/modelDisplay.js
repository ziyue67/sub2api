import { formatReasoningEffort } from './format';
const UPSTREAM_BRAND_PATTERN = /(?:^|[\s._-]+)(?:\(|\[)?\s*api[\s_-]*mart\b\s*(?:\)|\])?/gi;
function stripUpstreamBrand(value) {
    return value
        .replace(UPSTREAM_BRAND_PATTERN, '')
        .replace(/\(\s*\)|\[\s*\]/g, '')
        .replace(/\s{2,}/g, ' ')
        .replace(/[\s._-]+$/g, '')
        .replace(/^[\s._-]+/g, '')
        .trim();
}
export function cleanModelDisplayName(value, fallback = '模型') {
    const raw = typeof value === 'string' ? value.trim() : '';
    const cleaned = stripUpstreamBrand(raw);
    if (cleaned)
        return cleaned;
    const safeFallback = stripUpstreamBrand(fallback);
    return safeFallback || '模型';
}
export function displayModelLabel(model, label) {
    return cleanModelDisplayName(label || model || '', model || '模型');
}
export function displayModelWithReasoningEffort(model, reasoningEffort) {
    const modelLabel = displayModelLabel(model);
    const effortLabel = formatReasoningEffort(reasoningEffort);
    return effortLabel === '-' ? modelLabel : `${modelLabel} (${effortLabel})`;
}
export function displayModelChain(chain) {
    if (!chain)
        return '';
    return chain
        .split('→')
        .map((item) => displayModelLabel(item.trim(), item.trim()))
        .join('→');
}
