import { i18n } from '@/i18n';
import { getCustomMenuLabel } from '@/utils/customMenu';
/**
 * 统一生成页面标题，避免多处写入 document.title 产生覆盖冲突。
 * 优先使用 titleKey 通过 i18n 翻译，fallback 到静态 routeTitle。
 */
export function resolveDocumentTitle(routeTitle, siteName, titleKey) {
    const normalizedSiteName = typeof siteName === 'string' && siteName.trim() ? siteName.trim() : 'Sub2API';
    if (typeof titleKey === 'string' && titleKey.trim()) {
        const translated = i18n.global.t(titleKey);
        if (translated && translated !== titleKey) {
            return `${translated} - ${normalizedSiteName}`;
        }
    }
    if (typeof routeTitle === 'string' && routeTitle.trim()) {
        return `${routeTitle.trim()} - ${normalizedSiteName}`;
    }
    return normalizedSiteName;
}
/** /purchase 路由名，与 router/index.ts 中的声明保持一致。 */
export const PURCHASE_ROUTE_NAME = 'PurchaseSubscription';
/**
 * 解析路由的 i18n 标题/描述 key。绝大多数路由直接取 meta；
 * /purchase 随站点计费模式切换文案，AppHeader 与 document.title 共用这一处判断。
 */
export function resolveRouteMetaKeys(route, options = {}) {
    if (route.name === PURCHASE_ROUTE_NAME) {
        if (options.billingMode === 'recharge_only') {
            return { titleKey: 'nav.recharge', descriptionKey: 'purchase.rechargeDescription' };
        }
        if (options.billingMode === 'subscription_only') {
            return { titleKey: 'nav.subscribe', descriptionKey: 'purchase.subscriptionDescription' };
        }
    }
    return {
        titleKey: typeof route.meta.titleKey === 'string' ? route.meta.titleKey : undefined,
        descriptionKey: typeof route.meta.descriptionKey === 'string' ? route.meta.descriptionKey : undefined,
    };
}
export function resolveRouteDocumentTitle(route, siteName, customMenuItems = [], options = {}) {
    const id = typeof route.params.id === 'string' ? route.params.id : '';
    const menuItem = route.name === 'CustomPage' && id
        ? customMenuItems.find((item) => item.id === id)
        : undefined;
    const menuTitle = menuItem ? getCustomMenuLabel(menuItem, i18n.global.locale.value) : '';
    const { titleKey } = resolveRouteMetaKeys(route, options);
    return resolveDocumentTitle(menuTitle || route.meta.title, siteName, menuTitle ? undefined : titleKey);
}
