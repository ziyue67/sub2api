export const imagePricingPlatforms = new Set([
    "antigravity",
    "composite",
    "gemini",
    "grok",
    "openai",
]);
export const supportsImagePricingPlatform = (platform) => imagePricingPlatforms.has(platform);
// 与后端 DefaultAllowImageGenerationForPlatform 一致：这些平台新建分组默认允许生图。
const imageGenerationDefaultPlatforms = new Set(["antigravity", "gemini", "grok"]);
export const defaultAllowImageGenerationForPlatform = (platform) => imageGenerationDefaultPlatforms.has(platform);
export const supportsVideoPricingPlatform = (platform) => platform === "grok";
export const imagePricingI18nKey = (_platform, key) => `admin.groups.imagePricing.${key}`;
export const videoPricingI18nKey = (key) => `admin.groups.videoPricing.${key}`;
const defaultImagePricePlaceholders = {
    default: {
        image_price_1k: "0.134",
        image_price_2k: "0.201",
        image_price_4k: "0.268",
    },
    grok: {
        image_price_1k: "0.02",
        image_price_2k: "0.02",
        image_price_4k: "0.02",
    },
};
// 视频价为每秒单价（USD/s）。480p/720p 取 grok-imagine-video（文生视频实际走该模型）的
// 官方每秒价；1080p 仅 grok-imagine-video-1.5 图生视频支持，取 1.5 的每秒价。
const defaultVideoPricePlaceholders = {
    grok: {
        video_price_480p: "0.05",
        video_price_720p: "0.07",
        video_price_1080p: "0.25",
    },
};
export const getImagePricePlaceholder = (platform, tier) => {
    const card = defaultImagePricePlaceholders[platform] ?? defaultImagePricePlaceholders.default;
    return card[tier];
};
export const getVideoPricePlaceholder = (platform, tier) => {
    const card = defaultVideoPricePlaceholders[platform];
    return card?.[tier] ?? "";
};
export const getDefaultImagePreviewPrice = (platform, tier) => {
    const placeholder = getImagePricePlaceholder(platform, tier);
    if (placeholder === "") {
        return null;
    }
    const value = Number(placeholder);
    return Number.isFinite(value) ? value : null;
};
export const getDefaultVideoPreviewPrice = (platform, tier) => {
    const placeholder = getVideoPricePlaceholder(platform, tier);
    if (placeholder === "") {
        return null;
    }
    const value = Number(placeholder);
    return Number.isFinite(value) ? value : null;
};
