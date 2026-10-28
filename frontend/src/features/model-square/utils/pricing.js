export const PER_MILLION_TOKENS = 1000000;
export function formatTokenPrice(value) {
    if (value == null)
        return '未配置';
    const perMillion = value * PER_MILLION_TOKENS;
    return '$' + perMillion.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: perMillion < 1 ? 6 : 2 }) + '/M';
}
export function formatRequestPrice(value, billingMode) {
    if (value == null)
        return '未配置';
    return '$' + value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 6 }) + '/' + (billingMode === 'image' ? '张' : '次');
}
export function billingModeLabel(pricing) {
    switch (pricing?.billing_mode) {
        case 'image':
            return '图片计费';
        case 'per_request':
            return '按次计费';
        default:
            return 'Token 计费';
    }
}
export function isRequestBilling(pricing) {
    return pricing?.billing_mode === 'image' || pricing?.billing_mode === 'per_request';
}
export function fullPriceItems(pricing) {
    return [
        { label: '输入', value: pricing?.input_price },
        { label: '输出', value: pricing?.output_price },
        { label: '缓存写入', value: pricing?.cache_write_price },
        { label: '缓存读取', value: pricing?.cache_read_price },
        { label: '图片输入', value: pricing?.image_input_price },
        { label: '图片输出', value: pricing?.image_output_price },
    ];
}
export function getPerMillionTokensPrice(value) {
    if (value == null)
        return null;
    return value * PER_MILLION_TOKENS;
}
export function formatMultiplier(multiplier) {
    if (multiplier == null)
        return '';
    if (multiplier === 1)
        return '原价';
    if (multiplier < 1) {
        const discount = (multiplier * 10).toFixed(1).replace(/\.0$/, '');
        return `${discount} 折`;
    }
    return `${multiplier}x`;
}
