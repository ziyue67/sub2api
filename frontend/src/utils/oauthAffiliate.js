const OAUTH_AFFILIATE_CODE_KEY = 'oauth_aff_code';
const AFFILIATE_REFERRAL_CODE_KEY = 'affiliate_referral_code';
const AFFILIATE_REFERRAL_TTL_MS = 30 * 24 * 60 * 60 * 1000;
export function normalizeOAuthAffiliateCode(value) {
    const raw = Array.isArray(value) ? value[0] : value;
    return typeof raw === 'string' ? raw.trim() : '';
}
export function pickOAuthAffiliateCode(...values) {
    for (const value of values) {
        const code = normalizeOAuthAffiliateCode(value);
        if (code) {
            return code;
        }
    }
    return '';
}
export function storeAffiliateReferralCode(value, now = Date.now()) {
    if (typeof window === 'undefined') {
        return;
    }
    const code = normalizeOAuthAffiliateCode(value);
    if (!code) {
        return;
    }
    try {
        const payload = {
            code,
            expiresAt: now + AFFILIATE_REFERRAL_TTL_MS
        };
        window.localStorage.setItem(AFFILIATE_REFERRAL_CODE_KEY, JSON.stringify(payload));
    }
    catch {
        // 忽略浏览器存储异常。
    }
}
export function loadAffiliateReferralCode(now = Date.now()) {
    if (typeof window === 'undefined') {
        return '';
    }
    try {
        const raw = window.localStorage.getItem(AFFILIATE_REFERRAL_CODE_KEY);
        if (!raw) {
            return '';
        }
        const parsed = JSON.parse(raw);
        const code = normalizeOAuthAffiliateCode(parsed.code);
        const expiresAt = Number(parsed.expiresAt) || 0;
        if (!code || expiresAt <= now) {
            clearAffiliateReferralCode();
            return '';
        }
        return code;
    }
    catch {
        clearAffiliateReferralCode();
        return '';
    }
}
export function clearAffiliateReferralCode() {
    if (typeof window === 'undefined') {
        return;
    }
    try {
        window.localStorage.removeItem(AFFILIATE_REFERRAL_CODE_KEY);
    }
    catch {
        // 忽略浏览器存储异常。
    }
}
export function resolveAffiliateReferralCode(...values) {
    const code = pickOAuthAffiliateCode(...values);
    if (code) {
        storeAffiliateReferralCode(code);
        return code;
    }
    return loadAffiliateReferralCode();
}
export function storeOAuthAffiliateCode(value) {
    if (typeof window === 'undefined') {
        return;
    }
    const code = normalizeOAuthAffiliateCode(value);
    try {
        if (code) {
            window.sessionStorage.setItem(OAUTH_AFFILIATE_CODE_KEY, code);
        }
        else {
            window.sessionStorage.removeItem(OAUTH_AFFILIATE_CODE_KEY);
        }
    }
    catch {
        // 忽略浏览器存储异常。
    }
}
export function loadOAuthAffiliateCode() {
    if (typeof window === 'undefined') {
        return '';
    }
    try {
        return normalizeOAuthAffiliateCode(window.sessionStorage.getItem(OAUTH_AFFILIATE_CODE_KEY));
    }
    catch {
        return '';
    }
}
export function clearOAuthAffiliateCode() {
    if (typeof window === 'undefined') {
        return;
    }
    try {
        window.sessionStorage.removeItem(OAUTH_AFFILIATE_CODE_KEY);
    }
    catch {
        // 忽略浏览器存储异常。
    }
}
export function clearAllAffiliateReferralCodes() {
    clearOAuthAffiliateCode();
    clearAffiliateReferralCode();
}
export function oauthAffiliatePayload(value) {
    const code = normalizeOAuthAffiliateCode(value);
    return code ? { aff_code: code } : {};
}
