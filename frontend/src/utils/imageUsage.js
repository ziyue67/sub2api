/** Whether the row contains any image-output tokens. */
export const hasImageOutputTokens = (row) => (row?.image_output_tokens ?? 0) > 0;
/**
 * Text-only output tokens (total output minus image-output).
 * Returns 0 when no text tokens exist.
 */
export const textOutputTokens = (row) => Math.max(0, (row?.output_tokens ?? 0) - (row?.image_output_tokens ?? 0));
/** Whether the row has a non-zero image-output cost. */
export const hasImageOutputCost = (row) => (row?.image_output_cost ?? 0) > 0;
/** Whether the row contains any image-input tokens (e.g. gpt-image-2 image edits). */
export const hasImageInputTokens = (row) => (row?.image_input_tokens ?? 0) > 0;
/**
 * Text-only input tokens (total input minus image-input).
 * Returns 0 when no text tokens exist.
 */
export const textInputTokens = (row) => Math.max(0, (row?.input_tokens ?? 0) - (row?.image_input_tokens ?? 0));
/** Whether the row has a non-zero image-input cost. */
export const hasImageInputCost = (row) => (row?.image_input_cost ?? 0) > 0;
// --- Image size / billing helpers ---
const knownImageSizeSources = new Set(['output', 'input', 'default', 'legacy']);
const knownImageBillingSizes = new Set(['1K', '2K', '4K', 'mixed']);
const trimmed = (value) => value?.trim() ?? '';
export const formatImageBillingSize = (row, t) => {
    const size = trimmed(row?.image_size);
    if (!size) {
        return t('usage.imageSizeNotRecorded');
    }
    if (knownImageBillingSizes.has(size)) {
        return size;
    }
    return `${t('usage.imageSizeLegacyUnstandardized')}: ${size}`;
};
export const formatImageInputSize = (row, t) => {
    const size = trimmed(row?.image_input_size);
    return size || t('usage.imageSizeUnknown');
};
export const formatImageOutputSize = (row, t) => {
    const size = trimmed(row?.image_output_size);
    return size || t('usage.imageSizeUnknown');
};
export const formatImageSizeSource = (row, t) => {
    const source = trimmed(row?.image_size_source).toLowerCase();
    if (knownImageSizeSources.has(source)) {
        return t(`usage.imageSizeSource${source.charAt(0).toUpperCase()}${source.slice(1)}`);
    }
    if (trimmed(row?.image_size)) {
        return t('usage.imageSizeSourceLegacy');
    }
    return t('usage.imageSizeSourceMissing');
};
export const formatImageSizeBreakdown = (row) => {
    const breakdown = row?.image_size_breakdown;
    if (!breakdown || Object.keys(breakdown).length === 0) {
        return '';
    }
    return ['1K', '2K', '4K']
        .filter((tier) => (breakdown[tier] ?? 0) > 0)
        .map((tier) => `${tier} x ${breakdown[tier]}`)
        .join(', ');
};
