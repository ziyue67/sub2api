const normalizeUsageRefreshValue = (value) => {
    if (value == null)
        return '';
    return String(value);
};
const normalizeSnapshotRefreshValue = (value) => {
    if (Array.isArray(value)) {
        return value.map(normalizeSnapshotRefreshValue);
    }
    if (value && typeof value === 'object') {
        return Object.fromEntries(Object.entries(value)
            .filter(([, entry]) => entry !== undefined)
            .sort(([left], [right]) => left.localeCompare(right))
            .map(([key, entry]) => [key, normalizeSnapshotRefreshValue(entry)]));
    }
    return value;
};
const serializeSnapshotRefreshValue = (value) => {
    if (value == null)
        return '';
    return JSON.stringify(normalizeSnapshotRefreshValue(value)) ?? '';
};
const isNonBlankString = (value) => (typeof value === 'string' && value.trim().length > 0);
export const buildOpenAIUsageRefreshKey = (account) => {
    if (account.platform !== 'openai' || account.type !== 'oauth') {
        return '';
    }
    const extra = account.extra ?? {};
    return [
        account.id,
        account.updated_at,
        account.last_used_at,
        account.rate_limit_reset_at,
        extra.codex_usage_updated_at,
        extra.codex_5h_used_percent,
        extra.codex_5h_reset_at,
        extra.codex_5h_reset_after_seconds,
        extra.codex_5h_window_minutes,
        extra.codex_7d_used_percent,
        extra.codex_7d_reset_at,
        extra.codex_7d_reset_after_seconds,
        extra.codex_7d_window_minutes
    ].map(normalizeUsageRefreshValue).join('|');
};
export const buildGrokUsageRefreshKey = (account) => {
    if (account.platform !== 'grok') {
        return '';
    }
    const extra = account.extra ?? {};
    const usageSnapshot = extra.grok_usage_snapshot;
    const canonicalTier = usageSnapshot?.subscription_tier;
    const legacyQuotaFallback = isNonBlankString(canonicalTier)
        ? undefined
        : extra.grok_quota_snapshot;
    return [
        serializeSnapshotRefreshValue(extra.grok_billing_snapshot),
        serializeSnapshotRefreshValue(usageSnapshot),
        serializeSnapshotRefreshValue(legacyQuotaFallback)
    ].join('|');
};
