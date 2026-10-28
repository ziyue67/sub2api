import { shallowRef } from 'vue';
import builtinCatalog from './platformCatalog.builtin.json';
export const BUILTIN_PLATFORM_CATALOG = builtinCatalog;
const catalogState = shallowRef(BUILTIN_PLATFORM_CATALOG);
/** 替换当前清单（测试用于模拟新登记的平台）。 */
export function setPlatformCatalog(catalog) {
    catalogState.value = catalog;
}
/** 恢复内置清单（测试用）。 */
export function resetPlatformCatalog() {
    catalogState.value = BUILTIN_PLATFORM_CATALOG;
}
/** 全部具体平台，按展示顺序。 */
export function listPlatforms() {
    return catalogState.value.platforms;
}
export function listPlatformIds() {
    return listPlatforms().map(spec => spec.id);
}
export function getPlatformSpec(platform) {
    if (!platform)
        return undefined;
    return catalogState.value.platforms.find(spec => spec.id === platform);
}
export function isKnownPlatform(platform) {
    return !!getPlatformSpec(platform);
}
/** 与后端 buildUpstreamModelsRequest 支持的平台保持一致。 */
export function supportsUpstreamModelSync(platform) {
    const spec = getPlatformSpec(platform?.trim().toLowerCase());
    if (!spec)
        return false;
    return !!spec.multi_protocol || ['anthropic', 'openai', 'gemini', 'antigravity', 'grok'].includes(spec.id);
}
/** 清单中声明了定价目录 provider 的平台才支持同步定价模型。 */
export function supportsPricingModelSync(platform) {
    return !!getPlatformSpec(platform?.trim().toLowerCase())?.litellm_provider?.trim();
}
/** 平台展示名；未登记的值原样返回。 */
export function platformDisplayName(platform) {
    return getPlatformSpec(platform)?.display_name ?? platform ?? '';
}
export function compositePrecedencePlatformIds() {
    return catalogState.value.composite_precedence;
}
export function platformUsesGateway(platform, gateway) {
    return getPlatformSpec(platform)?.gateway === gateway;
}
export function getProviderProfile(platform) {
    return getPlatformSpec(platform)?.multi_protocol ?? undefined;
}
/** 指定接入模式的端点；未知模式回落默认模式（与后端 ProviderProfile.Endpoints 一致）。 */
export function getProviderMode(platform, mode) {
    const profile = getProviderProfile(platform);
    if (!profile)
        return undefined;
    const trimmed = (mode ?? '').trim();
    return (profile.modes.find(item => item.mode === trimmed) ??
        profile.modes.find(item => item.mode === profile.default_mode) ??
        profile.modes[0]);
}
