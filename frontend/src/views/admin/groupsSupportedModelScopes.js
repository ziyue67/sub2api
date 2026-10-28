export const normalizeSupportedModelScopesForPlatform = (platform, scopes) => {
    if (platform !== "antigravity")
        return [];
    return scopes ?? [];
};
