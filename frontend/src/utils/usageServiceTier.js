export function normalizeUsageServiceTier(serviceTier) {
    const value = serviceTier?.trim().toLowerCase();
    if (!value)
        return null;
    if (value === 'fast')
        return 'priority';
    if (value === 'default' || value === 'standard')
        return 'standard';
    if (value === 'priority' || value === 'flex' || value === 'ultrafast')
        return value;
    return value;
}
export function formatUsageServiceTier(serviceTier) {
    const normalized = normalizeUsageServiceTier(serviceTier);
    if (!normalized)
        return 'standard';
    return normalized;
}
export function getUsageServiceTierLabel(serviceTier, translate) {
    const tier = formatUsageServiceTier(serviceTier);
    if (tier === 'priority')
        return translate('usage.serviceTierPriority');
    if (tier === 'ultrafast')
        return translate('usage.serviceTierUltrafast');
    if (tier === 'flex')
        return translate('usage.serviceTierFlex');
    if (tier === 'standard')
        return translate('usage.serviceTierStandard');
    return tier;
}
