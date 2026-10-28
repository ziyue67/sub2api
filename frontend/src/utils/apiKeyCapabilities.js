const CHAT_PLATFORMS = new Set(['openai', 'anthropic', 'gemini', 'antigravity']);
function isActiveGroup(group) {
    return !!group && group.status !== 'inactive';
}
function groupSupportsChat(group) {
    return isActiveGroup(group) && CHAT_PLATFORMS.has(group.platform);
}
function groupSupportsOpenAIImage(group) {
    return isActiveGroup(group) && group.platform === 'openai' && group.allow_image_generation === true;
}
function groupSupportsVideo(group) {
    // 当前分支的 Group 没有 routing_scope，用视频计费配置作为能力信号：
    // 独立视频计费或配置了任一分辨率单价，即视为支持视频生成。
    if (!isActiveGroup(group))
        return false;
    return (group.video_rate_independent === true ||
        group.video_price_480p != null ||
        group.video_price_720p != null ||
        group.video_price_1080p != null);
}
// 收集一个 API Key 可路由到的全部活跃分组：主分组 + group_routes 中已启用的分组。
export function apiKeyGroups(key) {
    const groups = [];
    const seen = new Set();
    const append = (group) => {
        if (!isActiveGroup(group) || seen.has(group.id))
            return;
        seen.add(group.id);
        groups.push(group);
    };
    append(key.group);
    // group_routes 只携带 group_id/priority/weight/enabled，不含分组详情，
    // 因此仅在其引用的分组正好是主分组时可判定；其余分组详情由后端在 key.group 之外
    // 不下发。这里保留主分组作为唯一可解析的能力来源，避免误判。
    return groups;
}
export function apiKeyChatGroups(key) {
    return apiKeyGroups(key).filter(groupSupportsChat);
}
export function apiKeySupportsChat(key) {
    return key.status === 'active' && apiKeyChatGroups(key).length > 0;
}
export function apiKeySupportsOpenAI(key) {
    return key.status === 'active' && apiKeyGroups(key).some((group) => group.platform === 'openai');
}
export function apiKeyOpenAIImageGroups(key) {
    return apiKeyGroups(key).filter(groupSupportsOpenAIImage);
}
export function apiKeySupportsOpenAIImageGeneration(key) {
    return key.status === 'active' && apiKeyOpenAIImageGroups(key).length > 0;
}
export function apiKeyVideoGroups(key) {
    return apiKeyGroups(key).filter(groupSupportsVideo);
}
export function apiKeySupportsVideoGeneration(key) {
    return key.status === 'active' && apiKeyVideoGroups(key).length > 0;
}
export function apiKeyUnifiedAccessCapabilities(key) {
    return {
        chat: apiKeySupportsChat(key),
        image: apiKeySupportsOpenAIImageGeneration(key),
        video: apiKeySupportsVideoGeneration(key),
    };
}
export function apiKeySupportsUnifiedAccess(key) {
    const capabilities = apiKeyUnifiedAccessCapabilities(key);
    return capabilities.chat && capabilities.image;
}
export function primaryAPIKeyGroupName(key) {
    return key.group?.name || apiKeyGroups(key)[0]?.name || '';
}
export function primaryAPIKeyImageGroupName(key) {
    return apiKeyOpenAIImageGroups(key)[0]?.name || primaryAPIKeyGroupName(key);
}
