/**
 * Admin Settings API endpoints
 * Handles system settings management for administrators
 */
import { apiClient } from "../client";
import { listPlatformIds } from "@/constants/platformCatalog";
// 与后端 AllowedSchedulingThresholdPlatforms 保持一致（deepseek 为余额型，
// 走余额检测而非用量阈值；minimax Coding/Token Plan、OpenCode GO 与 Command Code 有滚动窗口）。
export const SCHEDULING_THRESHOLD_PLATFORMS = [
    "openai",
    "anthropic",
    "grok",
    "kimi",
    "zhipu",
    "minimax",
    "opencode_go",
    "command_code",
];
export function normalizeAccountSchedulingThresholdsMap(input) {
    const result = {};
    for (const platform of SCHEDULING_THRESHOLD_PLATFORMS) {
        const value = input?.[platform];
        result[platform] = typeof value === "number" && Number.isFinite(value)
            ? Math.min(100, Math.max(1, Math.trunc(value)))
            : 100;
    }
    return result;
}
export function sanitizeAccountSchedulingThresholdsMap(input) {
    return normalizeAccountSchedulingThresholdsMap(input);
}
/** 归一化为全部平台 × 3 窗口（缺失填 null），供模板非空绑定 */
export function normalizePlatformQuotasMap(input) {
    const result = {};
    for (const p of listPlatformIds()) {
        const src = input?.[p];
        result[p] = {
            daily: typeof src?.daily === "number" ? src.daily : null,
            weekly: typeof src?.weekly === "number" ? src.weekly : null,
            monthly: typeof src?.monthly === "number" ? src.monthly : null,
        };
    }
    return result;
}
/** 提交前清洗：非有限数/负数/空字符串 → null（保留 0 = 显式禁用），返回全部平台嵌套 map */
export function sanitizePlatformQuotasMap(input) {
    const clean = (v) => (typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : null);
    const result = {};
    for (const p of listPlatformIds()) {
        const src = input?.[p];
        result[p] = { daily: clean(src?.daily), weekly: clean(src?.weekly), monthly: clean(src?.monthly) };
    }
    return result;
}
const AUTH_SOURCE_TYPES = [
    "email",
    "linuxdo",
    "oidc",
    "wechat",
    "github",
    "google",
    "dingtalk",
];
const AUTH_SOURCE_DEFAULT_BALANCE = 0;
const AUTH_SOURCE_DEFAULT_CONCURRENCY = 5;
const PAYMENT_VISIBLE_METHOD_SOURCE_OPTIONS = {
    alipay: [
        { value: "", labelZh: "未配置", labelEn: "Not configured" },
        {
            value: "official_alipay",
            labelZh: "支付宝官方",
            labelEn: "Official Alipay",
        },
        {
            value: "easypay_alipay",
            labelZh: "易支付支付宝",
            labelEn: "EasyPay Alipay",
        },
    ],
    wxpay: [
        { value: "", labelZh: "未配置", labelEn: "Not configured" },
        {
            value: "official_wxpay",
            labelZh: "微信官方",
            labelEn: "Official WeChat Pay",
        },
        {
            value: "easypay_wxpay",
            labelZh: "易支付微信",
            labelEn: "EasyPay WeChat Pay",
        },
    ],
};
const PAYMENT_VISIBLE_METHOD_SOURCE_ALIASES = {
    alipay: {
        official_alipay: "official_alipay",
        alipay: "official_alipay",
        alipay_direct: "official_alipay",
        official: "official_alipay",
        easypay_alipay: "easypay_alipay",
        easypay: "easypay_alipay",
    },
    wxpay: {
        official_wxpay: "official_wxpay",
        wxpay: "official_wxpay",
        wxpay_direct: "official_wxpay",
        wechat: "official_wxpay",
        official: "official_wxpay",
        easypay_wxpay: "easypay_wxpay",
        easypay: "easypay_wxpay",
    },
};
const WECHAT_CONNECT_MODE_OPTIONS = [
    { value: "open", labelZh: "PC 应用", labelEn: "PC App" },
    {
        value: "mp",
        labelZh: "公众号",
        labelEn: "Official Account",
    },
    {
        value: "mobile",
        labelZh: "移动应用",
        labelEn: "Mobile App",
    },
];
const WECHAT_CONNECT_MODE_ALIASES = {
    open: "open",
    open_platform: "open",
    official: "open",
    wx_open: "open",
    mp: "mp",
    official_account: "mp",
    wechat_mp: "mp",
    mini_program: "mp",
    mobile: "mobile",
    mobile_app: "mobile",
    native_app: "mobile",
};
export function normalizeDefaultSubscriptionSettings(subscriptions) {
    if (!Array.isArray(subscriptions))
        return [];
    return subscriptions
        .filter((item) => item.group_id > 0 && item.validity_days > 0)
        .map((item) => ({
        group_id: Math.floor(item.group_id),
        validity_days: Math.min(36500, Math.max(1, Math.floor(item.validity_days))),
    }));
}
export function buildAuthSourceDefaultsState(settings) {
    const raw = settings;
    return AUTH_SOURCE_TYPES.reduce((acc, source) => {
        const subscriptions = raw[`auth_source_default_${source}_subscriptions`];
        acc[source] = {
            balance: Number(raw[`auth_source_default_${source}_balance`] ??
                AUTH_SOURCE_DEFAULT_BALANCE),
            concurrency: Math.max(1, Number(raw[`auth_source_default_${source}_concurrency`] ??
                AUTH_SOURCE_DEFAULT_CONCURRENCY)),
            subscriptions: normalizeDefaultSubscriptionSettings(Array.isArray(subscriptions)
                ? subscriptions
                : []),
            grant_on_signup: raw[`auth_source_default_${source}_grant_on_signup`] === true,
            grant_on_first_bind: raw[`auth_source_default_${source}_grant_on_first_bind`] === true,
            platform_quotas: normalizePlatformQuotasMap(raw[`auth_source_default_${source}_platform_quotas`]),
        };
        return acc;
    }, {});
}
export function appendAuthSourceDefaultsToUpdateRequest(payload, authSourceDefaults) {
    const target = payload;
    for (const source of AUTH_SOURCE_TYPES) {
        const current = authSourceDefaults[source];
        target[`auth_source_default_${source}_balance`] =
            Number(current.balance) || 0;
        target[`auth_source_default_${source}_concurrency`] = Math.max(1, Math.floor(Number(current.concurrency) || AUTH_SOURCE_DEFAULT_CONCURRENCY));
        target[`auth_source_default_${source}_subscriptions`] =
            normalizeDefaultSubscriptionSettings(current.subscriptions);
        target[`auth_source_default_${source}_grant_on_signup`] =
            current.grant_on_signup;
        target[`auth_source_default_${source}_grant_on_first_bind`] =
            current.grant_on_first_bind;
        target[`auth_source_default_${source}_platform_quotas`] = sanitizePlatformQuotasMap(current.platform_quotas);
    }
    return payload;
}
export function getPaymentVisibleMethodSourceOptions(method) {
    return PAYMENT_VISIBLE_METHOD_SOURCE_OPTIONS[method];
}
export function normalizePaymentVisibleMethodSource(method, source) {
    if (typeof source !== "string")
        return "";
    const normalized = source.trim().toLowerCase();
    if (!normalized)
        return "";
    return PAYMENT_VISIBLE_METHOD_SOURCE_ALIASES[method][normalized] ?? "";
}
export function getWeChatConnectModeOptions() {
    return WECHAT_CONNECT_MODE_OPTIONS;
}
export function normalizeWeChatConnectMode(source) {
    if (typeof source !== "string")
        return "open";
    const normalized = source.trim().toLowerCase();
    if (!normalized)
        return "open";
    return WECHAT_CONNECT_MODE_ALIASES[normalized] ?? "open";
}
export function defaultWeChatConnectScopesForMode(mode) {
    switch (normalizeWeChatConnectMode(mode)) {
        case "mp":
            return "snsapi_userinfo";
        case "mobile":
            return "";
        default:
            return "snsapi_login";
    }
}
export function resolveWeChatConnectModeCapabilities(openEnabled, mpEnabled, mobileEnabled, legacyMode) {
    if (typeof openEnabled === "boolean" ||
        typeof mpEnabled === "boolean" ||
        typeof mobileEnabled === "boolean") {
        return {
            openEnabled: openEnabled === true,
            mpEnabled: mpEnabled === true,
            mobileEnabled: mobileEnabled === true,
        };
    }
    switch (normalizeWeChatConnectMode(legacyMode)) {
        case "mp":
            return { openEnabled: false, mpEnabled: true, mobileEnabled: false };
        case "mobile":
            return { openEnabled: false, mpEnabled: false, mobileEnabled: true };
        default:
            return { openEnabled: true, mpEnabled: false, mobileEnabled: false };
    }
}
export function deriveWeChatConnectStoredMode(openEnabled, mpEnabled, mobileEnabled, legacyMode) {
    if (mpEnabled)
        return "mp";
    if (mobileEnabled)
        return "mobile";
    if (openEnabled)
        return "open";
    return normalizeWeChatConnectMode(legacyMode);
}
/**
 * Get all system settings
 * @returns System settings
 */
export async function getSettings() {
    const { data } = await apiClient.get("/admin/settings");
    return data;
}
/**
 * Update system settings
 * @param settings - Partial settings to update
 * @returns Updated settings
 */
export async function updateSettings(settings) {
    const { data } = await apiClient.put("/admin/settings", settings);
    return data;
}
/**
 * Test SMTP connection with provided config
 * @param config - SMTP configuration to test
 * @returns Test result message
 */
export async function testSmtpConnection(config) {
    const { data } = await apiClient.post("/admin/settings/test-smtp", config);
    return data;
}
/**
 * Send test email with provided SMTP config
 * @param request - Email address and SMTP config
 * @returns Test result message
 */
export async function sendTestEmail(request) {
    const { data } = await apiClient.post("/admin/settings/send-test-email", request);
    return data;
}
export async function getEmailTemplates() {
    const { data } = await apiClient.get("/admin/settings/email-templates");
    return data;
}
export async function getEmailTemplate(event, locale) {
    const { data } = await apiClient.get(`/admin/settings/email-templates/${encodeURIComponent(event)}/${encodeURIComponent(locale)}`);
    return data;
}
export async function updateEmailTemplate(event, locale, request) {
    const { data } = await apiClient.put(`/admin/settings/email-templates/${encodeURIComponent(event)}/${encodeURIComponent(locale)}`, request);
    return data;
}
export async function restoreOfficialEmailTemplate(event, locale) {
    const { data } = await apiClient.post(`/admin/settings/email-templates/${encodeURIComponent(event)}/${encodeURIComponent(locale)}/restore-official`);
    return data;
}
export async function previewEmailTemplate(request) {
    const { data } = await apiClient.post("/admin/settings/email-template-preview", request);
    return data;
}
/**
 * Get admin API key status
 * @returns Status indicating if key exists and masked version
 */
export async function getAdminApiKey() {
    const { data } = await apiClient.get("/admin/settings/admin-api-key");
    return data;
}
/**
 * Regenerate admin API key
 * @returns The new full API key (only shown once)
 */
export async function regenerateAdminApiKey() {
    const { data } = await apiClient.post("/admin/settings/admin-api-key/regenerate");
    return data;
}
/**
 * Delete admin API key
 * @returns Success message
 */
export async function deleteAdminApiKey() {
    const { data } = await apiClient.delete("/admin/settings/admin-api-key");
    return data;
}
export async function getOverloadCooldownSettings() {
    const { data } = await apiClient.get("/admin/settings/overload-cooldown");
    return data;
}
export async function updateOverloadCooldownSettings(settings) {
    const { data } = await apiClient.put("/admin/settings/overload-cooldown", settings);
    return data;
}
export async function getRateLimit429CooldownSettings() {
    const { data } = await apiClient.get("/admin/settings/rate-limit-429-cooldown");
    return data;
}
export async function updateRateLimit429CooldownSettings(settings) {
    const { data } = await apiClient.put("/admin/settings/rate-limit-429-cooldown", settings);
    return data;
}
export async function getPanelRateLimitSettings() {
    const { data } = await apiClient.get("/admin/settings/panel-rate-limit");
    return data;
}
export async function updatePanelRateLimitSettings(settings) {
    const { data } = await apiClient.put("/admin/settings/panel-rate-limit", settings);
    return data;
}
/**
 * Get stream timeout settings
 * @returns Stream timeout settings
 */
export async function getStreamTimeoutSettings() {
    const { data } = await apiClient.get("/admin/settings/stream-timeout");
    return data;
}
/**
 * Update stream timeout settings
 * @param settings - Stream timeout settings to update
 * @returns Updated settings
 */
export async function updateStreamTimeoutSettings(settings) {
    const { data } = await apiClient.put("/admin/settings/stream-timeout", settings);
    return data;
}
/**
 * Get rectifier settings
 * @returns Rectifier settings
 */
export async function getRectifierSettings() {
    const { data } = await apiClient.get("/admin/settings/rectifier");
    return data;
}
/**
 * Update rectifier settings
 * @param settings - Rectifier settings to update
 * @returns Updated settings
 */
export async function updateRectifierSettings(settings) {
    const { data } = await apiClient.put("/admin/settings/rectifier", settings);
    return data;
}
/**
 * Get beta policy settings
 * @returns Beta policy settings
 */
export async function getBetaPolicySettings() {
    const { data } = await apiClient.get("/admin/settings/beta-policy");
    return data;
}
/**
 * Update beta policy settings
 * @param settings - Beta policy settings to update
 * @returns Updated settings
 */
export async function updateBetaPolicySettings(settings) {
    const { data } = await apiClient.put("/admin/settings/beta-policy", settings);
    return data;
}
export async function getWebSearchEmulationConfig() {
    const { data } = await apiClient.get("/admin/settings/web-search-emulation");
    return data;
}
export async function updateWebSearchEmulationConfig(config) {
    const { data } = await apiClient.put("/admin/settings/web-search-emulation", config);
    return data;
}
export async function testWebSearchEmulation(query) {
    const { data } = await apiClient.post("/admin/settings/web-search-emulation/test", { query });
    return data;
}
export async function resetWebSearchUsage(payload) {
    await apiClient.post("/admin/settings/web-search-emulation/reset-usage", payload);
}
export const settingsAPI = {
    getSettings,
    updateSettings,
    testSmtpConnection,
    sendTestEmail,
    getEmailTemplates,
    getEmailTemplate,
    updateEmailTemplate,
    restoreOfficialEmailTemplate,
    previewEmailTemplate,
    getAdminApiKey,
    regenerateAdminApiKey,
    deleteAdminApiKey,
    getOverloadCooldownSettings,
    updateOverloadCooldownSettings,
    getRateLimit429CooldownSettings,
    updateRateLimit429CooldownSettings,
    getPanelRateLimitSettings,
    updatePanelRateLimitSettings,
    getStreamTimeoutSettings,
    updateStreamTimeoutSettings,
    getRectifierSettings,
    updateRectifierSettings,
    getBetaPolicySettings,
    updateBetaPolicySettings,
    getWebSearchEmulationConfig,
    updateWebSearchEmulationConfig,
    testWebSearchEmulation,
    resetWebSearchUsage,
};
export default settingsAPI;
