export const grokVideoPriceResolutions = [
    { key: '480p', label: '480p' },
    { key: '720p', label: '720p' },
    { key: '1080p', label: '1080p' }
];
export const grokVideoPriceFamilies = [
    { key: 'grok-imagine-video', label: 'grok-imagine-video' },
    { key: 'grok-imagine-video-1.5', label: 'grok-imagine-video-1.5' }
];
function normalizeFamily(value) {
    return value.trim().toLowerCase();
}
function normalizePrice(value) {
    if (value === null || value === undefined || value === '')
        return null;
    const price = Number(value);
    return Number.isFinite(price) && price >= 0 ? price : null;
}
function emptyTiers() {
    return Object.fromEntries(grokVideoPriceResolutions.map(({ key }) => [key, null]));
}
// Keep unknown families from an existing group so a future backend catalog is
// not silently discarded when an operator edits another group setting.
export function createVideoModelPricesForm(prices) {
    const form = {};
    for (const [rawFamily, rawTiers] of Object.entries(prices ?? {})) {
        const family = normalizeFamily(rawFamily);
        if (!family || !rawTiers || typeof rawTiers !== 'object')
            continue;
        form[family] = emptyTiers();
        for (const [rawResolution, rawPrice] of Object.entries(rawTiers)) {
            const price = normalizePrice(rawPrice);
            if (price !== null)
                form[family][rawResolution.trim().toLowerCase()] = price;
        }
    }
    for (const { key } of grokVideoPriceFamilies) {
        form[key] ?? (form[key] = emptyTiers());
    }
    return form;
}
export function serializeVideoModelPrices(form) {
    const result = {};
    for (const [rawFamily, tiers] of Object.entries(form)) {
        const family = normalizeFamily(rawFamily);
        if (!family || !tiers || typeof tiers !== 'object')
            continue;
        const normalizedTiers = {};
        for (const [rawResolution, rawPrice] of Object.entries(tiers)) {
            const resolution = rawResolution.trim().toLowerCase();
            const price = normalizePrice(rawPrice);
            if (resolution && price !== null)
                normalizedTiers[resolution] = price;
        }
        if (Object.keys(normalizedTiers).length > 0)
            result[family] = normalizedTiers;
    }
    return result;
}
export function videoModelPriceFamilyRows(form) {
    const known = new Set(grokVideoPriceFamilies.map(({ key }) => key));
    const extra = Object.keys(form)
        .map(normalizeFamily)
        .filter((family) => family && !known.has(family))
        .sort()
        .map((key) => ({ key, label: key }));
    return [...grokVideoPriceFamilies, ...extra];
}
