const DEFAULT_CODEX_CLIENT_VERSION = '0.158.0';
function normalizeCodexBaseUrl(baseUrl) {
    const fallback = typeof window !== 'undefined' ? window.location.origin : '';
    const value = (baseUrl || fallback).trim().replace(/\/+$/, '');
    if (!value)
        return '/v1';
    return /\/v1$/i.test(value) ? value : `${value}/v1`;
}
export function buildCodexModelsManifestUrl(baseUrl, clientVersion = DEFAULT_CODEX_CLIENT_VERSION) {
    const params = new URLSearchParams({ client_version: clientVersion });
    return `${buildCodexModelCatalogUrl(baseUrl)}?${params.toString()}`;
}
export function buildCodexModelCatalogUrl(baseUrl) {
    return `${normalizeCodexBaseUrl(baseUrl)}/models`;
}
function isCodexModelsManifest(value) {
    return typeof value === 'object' && value !== null && Array.isArray(value.models);
}
export async function fetchCodexModelsManifest(baseUrl, apiKey, signal) {
    const response = await fetch(buildCodexModelsManifestUrl(baseUrl), {
        method: 'GET',
        headers: {
            Accept: 'application/json',
            Authorization: `Bearer ${apiKey}`
        },
        cache: 'no-store',
        signal
    });
    if (!response.ok) {
        throw new Error(`Codex models request failed with status ${response.status}`);
    }
    const text = await response.text();
    const payload = JSON.parse(text);
    if (!isCodexModelsManifest(payload)) {
        throw new Error('Codex models response is not a valid manifest');
    }
    return {
        content: JSON.stringify(payload, null, 2),
        modelCount: payload.models.length,
        responseBytes: new TextEncoder().encode(text).byteLength
    };
}
