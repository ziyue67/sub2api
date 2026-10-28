export const OPENAI_WS_MODE_OFF = 'off';
export const OPENAI_WS_MODE_CTX_POOL = 'ctx_pool';
export const OPENAI_WS_MODE_PASSTHROUGH = 'passthrough';
export const OPENAI_WS_MODE_HTTP_BRIDGE = 'http_bridge';
const OPENAI_WS_MODES = new Set([
    OPENAI_WS_MODE_OFF,
    OPENAI_WS_MODE_CTX_POOL,
    OPENAI_WS_MODE_PASSTHROUGH,
    OPENAI_WS_MODE_HTTP_BRIDGE
]);
export const normalizeOpenAIWSMode = (mode) => {
    if (typeof mode !== 'string')
        return null;
    const normalized = mode.trim().toLowerCase();
    if (normalized === 'shared' || normalized === 'dedicated') {
        return OPENAI_WS_MODE_CTX_POOL;
    }
    if (OPENAI_WS_MODES.has(normalized)) {
        return normalized;
    }
    return null;
};
export const openAIWSModeFromEnabled = (enabled) => {
    if (typeof enabled !== 'boolean')
        return null;
    return enabled ? OPENAI_WS_MODE_CTX_POOL : OPENAI_WS_MODE_OFF;
};
export const isOpenAIWSModeEnabled = (mode) => {
    return mode !== OPENAI_WS_MODE_OFF;
};
export const resolveOpenAIWSModeHintKey = (mode) => {
    switch (mode) {
        case OPENAI_WS_MODE_CTX_POOL:
            return 'admin.accounts.openai.wsModeCtxPoolHint';
        case OPENAI_WS_MODE_PASSTHROUGH:
            return 'admin.accounts.openai.wsModePassthroughHint';
        case OPENAI_WS_MODE_HTTP_BRIDGE:
            return 'admin.accounts.openai.wsModeHttpBridgeHint';
        default:
            return null;
    }
};
export const resolveOpenAIWSModeFromExtra = (extra, options) => {
    const fallback = options.defaultMode ?? OPENAI_WS_MODE_OFF;
    if (!extra)
        return fallback;
    const mode = normalizeOpenAIWSMode(extra[options.modeKey]);
    if (mode)
        return mode;
    const enabledMode = openAIWSModeFromEnabled(extra[options.enabledKey]);
    if (enabledMode)
        return enabledMode;
    const fallbackKeys = options.fallbackEnabledKeys ?? [];
    for (const key of fallbackKeys) {
        const modeFromFallbackKey = openAIWSModeFromEnabled(extra[key]);
        if (modeFromFallbackKey)
            return modeFromFallbackKey;
    }
    return fallback;
};
