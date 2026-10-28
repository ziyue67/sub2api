export const DEFAULT_PAYMENT_CURRENCY = 'CNY';
const PAYMENT_CURRENCY_SYMBOLS = {
    USD: '$',
    CNY: '¥',
    RMB: '¥',
    EUR: '€',
    GBP: '£',
    JPY: '¥',
    HKD: 'HK$',
    TWD: 'NT$',
    KRW: '₩',
    AUD: 'A$',
    CAD: 'C$',
    SGD: 'S$',
    NZD: 'NZ$',
    MOP: 'MOP$',
    MYR: 'RM',
    THB: '฿',
    PHP: '₱',
    INR: '₹',
};
export function normalizePaymentCurrency(currency) {
    const normalized = String(currency || '').trim().toUpperCase();
    return /^[A-Z]{3}$/.test(normalized) ? normalized : DEFAULT_PAYMENT_CURRENCY;
}
export function currencySymbol(currency) {
    const normalized = normalizePaymentCurrency(currency);
    return PAYMENT_CURRENCY_SYMBOLS[normalized] || normalized;
}
function paymentCurrencyFractionDigits(currency) {
    try {
        return new Intl.NumberFormat(undefined, {
            style: 'currency',
            currency,
        }).resolvedOptions().maximumFractionDigits ?? 2;
    }
    catch {
        return 2;
    }
}
export function formatPaymentAmount(amount, currency, locale) {
    const normalized = normalizePaymentCurrency(currency);
    const fractionDigits = paymentCurrencyFractionDigits(normalized);
    try {
        return new Intl.NumberFormat(locale || undefined, {
            style: 'currency',
            currency: normalized,
            currencyDisplay: 'narrowSymbol',
            minimumFractionDigits: fractionDigits,
            maximumFractionDigits: fractionDigits,
        }).format(Number.isFinite(amount) ? amount : 0);
    }
    catch {
        return `${normalized} ${(Number.isFinite(amount) ? amount : 0).toFixed(fractionDigits)}`;
    }
}
