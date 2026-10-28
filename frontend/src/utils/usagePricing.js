export const TOKENS_PER_MILLION = 1000000;
function isFiniteNumber(value) {
    return typeof value === 'number' && Number.isFinite(value);
}
export function calculateTokenUnitPrice(cost, tokens) {
    if (!isFiniteNumber(cost) || !isFiniteNumber(tokens) || tokens <= 0) {
        return null;
    }
    return cost / tokens;
}
export function calculateTokenPricePerMillion(cost, tokens) {
    const unitPrice = calculateTokenUnitPrice(cost, tokens);
    if (unitPrice == null) {
        return null;
    }
    return unitPrice * TOKENS_PER_MILLION;
}
export function formatTokenPricePerMillion(cost, tokens, options = {}) {
    const pricePerMillion = calculateTokenPricePerMillion(cost, tokens);
    if (pricePerMillion == null) {
        return options.emptyValue ?? '-';
    }
    const fractionDigits = options.fractionDigits ?? 4;
    const formatted = pricePerMillion.toFixed(fractionDigits);
    return options.withCurrencySymbol == false ? formatted : `$${formatted}`;
}
