const GENERIC_UPSTREAM_MESSAGES = new Set([
    'upstream request failed',
    'upstream request failed after retries',
    'upstream gateway error',
    'upstream service temporarily unavailable'
]);
function parseGatewayErrorBody(raw) {
    const text = String(raw || '').trim();
    if (!text)
        return null;
    try {
        const parsed = JSON.parse(text);
        const err = parsed?.error;
        if (!err || typeof err !== 'object')
            return null;
        const type = typeof err.type === 'string' ? err.type.trim() : '';
        const message = typeof err.message === 'string' ? err.message.trim() : '';
        if (!type && !message)
            return null;
        return { type, message };
    }
    catch {
        return null;
    }
}
function isGenericGatewayUpstreamError(raw) {
    const parsed = parseGatewayErrorBody(raw);
    if (!parsed)
        return false;
    if (parsed.type !== 'upstream_error')
        return false;
    return GENERIC_UPSTREAM_MESSAGES.has(parsed.message.toLowerCase());
}
export function resolveUpstreamPayload(detail) {
    if (!detail)
        return '';
    const candidates = [
        detail.upstream_error_detail,
        detail.upstream_errors,
        detail.upstream_error_message
    ];
    for (const candidate of candidates) {
        const payload = String(candidate || '').trim();
        if (!payload)
            continue;
        // Normalize common "empty but present" JSON placeholders.
        if (payload === '[]' || payload === '{}' || payload.toLowerCase() === 'null') {
            continue;
        }
        return payload;
    }
    return '';
}
export function resolvePrimaryResponseBody(detail, errorType) {
    if (!detail)
        return '';
    const upstreamPayload = resolveUpstreamPayload(detail);
    const errorBody = String(detail.error_body || '').trim();
    if (errorType === 'upstream') {
        return upstreamPayload || errorBody;
    }
    if (!errorBody) {
        return upstreamPayload;
    }
    // For request detail modal, keep client-visible body by default.
    // But if that body is a generic gateway wrapper, show upstream payload first.
    if (upstreamPayload && isGenericGatewayUpstreamError(errorBody)) {
        return upstreamPayload;
    }
    return errorBody;
}
