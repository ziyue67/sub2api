function extractErrorMessage(error) {
    const err = (error || {});
    return err.response?.data?.detail || err.response?.data?.message || err.message || '';
}
export function buildAuthErrorMessage(error, options) {
    const { fallback } = options;
    const message = extractErrorMessage(error);
    return message || fallback;
}
