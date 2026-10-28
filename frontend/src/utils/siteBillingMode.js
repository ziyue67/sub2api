import { FeatureFlags, resolveFeatureFlag } from '@/utils/featureFlags';
export const SITE_BILLING_MODES = [
    'recharge_and_subscription',
    'recharge_only',
    'subscription_only',
];
/** i18n 子键（admin.settings.features.siteBillingMode.options / hints）。 */
export const SITE_BILLING_MODE_I18N_KEYS = {
    recharge_and_subscription: 'rechargeAndSubscription',
    recharge_only: 'rechargeOnly',
    subscription_only: 'subscriptionOnly',
};
export function resolveSiteBillingMode(settings) {
    const subscriptionEnabled = resolveFeatureFlag({ subscription_enabled: settings?.subscription_enabled }, FeatureFlags.subscription);
    if (!subscriptionEnabled)
        return 'recharge_only';
    if (settings?.payment_balance_disabled === true)
        return 'subscription_only';
    return 'recharge_and_subscription';
}
export function billingModeToSettings(mode) {
    switch (mode) {
        case 'recharge_only':
            return { subscription_enabled: false, payment_balance_disabled: false };
        case 'subscription_only':
            return { subscription_enabled: true, payment_balance_disabled: true };
        default:
            return { subscription_enabled: true, payment_balance_disabled: false };
    }
}
