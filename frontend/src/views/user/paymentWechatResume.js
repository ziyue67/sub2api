import { normalizeVisibleMethod } from '@/components/payment/paymentFlow';
function readQueryString(query, key) {
    const value = query[key];
    if (Array.isArray(value)) {
        return typeof value[0] === 'string' ? value[0] : '';
    }
    return typeof value === 'string' ? value : '';
}
export function hasWechatResumeQuery(query) {
    if (readQueryString(query, 'wechat_resume') === '1') {
        return true;
    }
    return readQueryString(query, 'wechat_resume_token') !== ''
        || readQueryString(query, 'openid') !== '';
}
export function parseWechatResumeRoute(query, plans, fallbackBalanceAmount) {
    if (!hasWechatResumeQuery(query)) {
        return null;
    }
    const wechatResumeToken = readQueryString(query, 'wechat_resume_token');
    const paymentType = normalizeVisibleMethod(readQueryString(query, 'payment_type')) || 'wxpay';
    const planId = Number.parseInt(readQueryString(query, 'plan_id'), 10);
    const hasPlanId = Number.isFinite(planId) && planId > 0;
    const orderType = readQueryString(query, 'order_type') === 'subscription' || hasPlanId
        ? 'subscription'
        : 'balance';
    const rawRenewalMode = readQueryString(query, 'renewal_mode');
    const renewalMode = rawRenewalMode === 'extend' ? 'extend' : 'restart';
    if (wechatResumeToken) {
        return {
            wechatResumeToken,
            paymentType,
            orderType,
            orderAmount: 0,
            planId: hasPlanId ? planId : undefined,
            renewalMode: orderType === 'subscription' ? renewalMode : undefined,
        };
    }
    const openid = readQueryString(query, 'openid');
    if (!openid) {
        return null;
    }
    const rawAmount = Number.parseFloat(readQueryString(query, 'amount'));
    const orderAmount = Number.isFinite(rawAmount) && rawAmount > 0
        ? rawAmount
        : (orderType === 'subscription'
            ? (plans.find(plan => plan.id === planId)?.price ?? 0)
            : fallbackBalanceAmount);
    return {
        openid,
        paymentType,
        orderType,
        orderAmount,
        planId: hasPlanId ? planId : undefined,
        renewalMode: orderType === 'subscription' ? renewalMode : undefined,
    };
}
export function stripWechatResumeQuery(query) {
    const nextQuery = { ...query };
    delete nextQuery.wechat_resume;
    delete nextQuery.wechat_resume_token;
    delete nextQuery.openid;
    delete nextQuery.state;
    delete nextQuery.scope;
    delete nextQuery.payment_type;
    delete nextQuery.amount;
    delete nextQuery.order_type;
    delete nextQuery.plan_id;
    delete nextQuery.renewal_mode;
    return nextQuery;
}
