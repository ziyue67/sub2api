const VALID_REQUEST_TYPES = new Set(['unknown', 'sync', 'stream', 'ws_v2', 'cyber', 'live']);
export const isUsageRequestType = (value) => {
    return typeof value === 'string' && VALID_REQUEST_TYPES.has(value);
};
export const resolveUsageRequestType = (value) => {
    if (isUsageRequestType(value.request_type)) {
        return value.request_type;
    }
    if (value.openai_ws_mode) {
        return 'ws_v2';
    }
    return value.stream ? 'stream' : 'sync';
};
export const requestTypeToLegacyStream = (requestType) => {
    // cyber 与 stream 正交（cyber 可发生在 stream 或非 stream 请求），不映射到 legacy stream 维度。
    if (!requestType || requestType === 'unknown' || requestType === 'cyber' || requestType === 'live') {
        return null;
    }
    if (requestType === 'sync') {
        return false;
    }
    return true;
};
