export const DEFAULT_ACCOUNT_COST_MULTIPLIER = 0.1;
export function isValidAccountCostMultiplier(value) {
    return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 1000000;
}
export function readAccountCostMultiplier(extra) {
    const value = extra?.cost_multiplier;
    return isValidAccountCostMultiplier(value) ? value : DEFAULT_ACCOUNT_COST_MULTIPLIER;
}
