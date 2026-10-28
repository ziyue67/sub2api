export const BILLING_MODE_TOKEN = 'token';
export const BILLING_MODE_PER_REQUEST = 'per_request';
export const BILLING_MODE_IMAGE = 'image';
export const BILLING_MODE_VIDEO = 'video';
export function getBillingModeLabel(mode, t) {
    switch (mode) {
        case BILLING_MODE_PER_REQUEST: return t('admin.usage.billingModePerRequest');
        case BILLING_MODE_IMAGE: return t('admin.usage.billingModeImage');
        case BILLING_MODE_VIDEO: return t('admin.usage.billingModeVideo');
        default: return t('admin.usage.billingModeToken');
    }
}
export function getBillingModeBadgeClass(mode) {
    switch (mode) {
        case BILLING_MODE_PER_REQUEST: return 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-300';
        case BILLING_MODE_IMAGE: return 'bg-pink-100 text-pink-700 dark:bg-pink-900/30 dark:text-pink-300';
        case BILLING_MODE_VIDEO: return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300';
        default: return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300';
    }
}
export function isImageUsage(row) {
    return (row?.image_count ?? 0) > 0 && row?.billing_mode !== BILLING_MODE_TOKEN && row?.billing_mode !== BILLING_MODE_VIDEO;
}
export function getDisplayBillingMode(row) {
    // Explicit video/token modes always win over image_count heuristics.
    if (row?.billing_mode === BILLING_MODE_VIDEO || row?.billing_mode === BILLING_MODE_TOKEN) {
        return row.billing_mode;
    }
    if ((row?.image_count ?? 0) > 0 && !row?.billing_mode) {
        return BILLING_MODE_IMAGE;
    }
    return row?.billing_mode;
}
export function imageUnitPrice(row) {
    if (!row || row.image_count <= 0)
        return 0;
    const total = row.total_cost ?? 0;
    const price = total / row.image_count;
    return Number.isFinite(price) ? price : 0;
}
