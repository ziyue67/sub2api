export function supportsGroupOpenAIFast(platform) {
    return platform === "openai" || platform === "composite";
}
export function normalizeGroupOpenAIFast(platform, enabled) {
    return supportsGroupOpenAIFast(platform) && enabled;
}
