export const DEFAULT_BPS_RECOVERY_INTERVAL_MINUTES = 60;
export const MAX_BPS_RECOVERY_INTERVAL_MINUTES = 10080;
export function isValidBPSRecoveryInterval(value) {
    return typeof value === 'number' && Number.isInteger(value) && value >= 1 && value <= MAX_BPS_RECOVERY_INTERVAL_MINUTES;
}
export function bpsRecoveryIntervalOrDefault(value) {
    return isValidBPSRecoveryInterval(value) ? value : DEFAULT_BPS_RECOVERY_INTERVAL_MINUTES;
}
