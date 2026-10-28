/**
 * Admin Accounts API endpoints
 * Handles AI platform account management for administrators
 */
import { apiClient, buildApiUrl } from '../client';
import { ADMIN_UI_REQUEST_HEADER } from '../adminUIRequest';
export { getCodexHarvestControls, saveCodexHarvestControls, getCodexHarvestNodes, resetCodexHarvestNodes } from './codexHarvest';
/**
 * List all accounts with pagination
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @param filters - Optional filters
 * @returns Paginated list of accounts
 */
export async function list(page = 1, pageSize = 20, filters, options) {
    const { data } = await apiClient.get('/admin/accounts', {
        params: {
            page,
            page_size: pageSize,
            ...filters
        },
        signal: options?.signal
    });
    return data;
}
export async function getUpstreamBillingRatesWithEtag(page = 1, pageSize = 20, filters, options) {
    const headers = {};
    if (options?.etag)
        headers['If-None-Match'] = options.etag;
    const response = await apiClient.get('/admin/accounts/upstream-billing-rates', {
        params: { page, page_size: pageSize, ...filters },
        headers,
        signal: options?.signal,
        validateStatus: (status) => (status >= 200 && status < 300) || status === 304
    });
    const etagHeader = typeof response.headers?.etag === 'string' ? response.headers.etag : null;
    if (response.status === 304)
        return { notModified: true, etag: etagHeader, data: null };
    return { notModified: false, etag: etagHeader, data: response.data };
}
export async function listWithEtag(page = 1, pageSize = 20, filters, options) {
    const headers = {};
    if (options?.etag) {
        headers['If-None-Match'] = options.etag;
    }
    const response = await apiClient.get('/admin/accounts', {
        params: {
            page,
            page_size: pageSize,
            ...filters
        },
        headers,
        signal: options?.signal,
        validateStatus: (status) => (status >= 200 && status < 300) || status === 304
    });
    const etagHeader = typeof response.headers?.etag === 'string' ? response.headers.etag : null;
    if (response.status === 304) {
        return {
            notModified: true,
            etag: etagHeader,
            data: null
        };
    }
    return {
        notModified: false,
        etag: etagHeader,
        data: response.data
    };
}
/**
 * Get account by ID
 * @param id - Account ID
 * @returns Account details
 */
export async function getById(id) {
    const { data } = await apiClient.get(`/admin/accounts/${id}`);
    return data;
}
/**
 * Create new account
 * @param accountData - Account data
 * @returns Created account
 */
export async function create(accountData) {
    const { data } = await apiClient.post('/admin/accounts', accountData);
    return data;
}
/**
 * Duplicate an account while keeping credentials on the server.
 * @param id - Source account ID
 * @returns Newly created account
 */
const duplicateOperationKeys = new Map();
function duplicateOperationStorageKey(id) {
    return `sub2api:admin:account-duplicate:${id}`;
}
function getStoredDuplicateOperationKey(id) {
    try {
        return globalThis.sessionStorage?.getItem(duplicateOperationStorageKey(id)) ?? null;
    }
    catch {
        return null;
    }
}
function storeDuplicateOperationKey(id, key) {
    try {
        if (key)
            globalThis.sessionStorage?.setItem(duplicateOperationStorageKey(id), key);
        else
            globalThis.sessionStorage?.removeItem(duplicateOperationStorageKey(id));
    }
    catch {
        // In-memory retry protection still works when browser storage is unavailable.
    }
}
export async function duplicate(id) {
    let idempotencyKey = duplicateOperationKeys.get(id) ?? getStoredDuplicateOperationKey(id);
    if (!idempotencyKey) {
        const requestID = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`;
        idempotencyKey = `account-duplicate-${id}-${requestID}`;
    }
    duplicateOperationKeys.set(id, idempotencyKey);
    storeDuplicateOperationKey(id, idempotencyKey);
    const { data } = await apiClient.post(`/admin/accounts/${id}/duplicate`, undefined, {
        headers: { 'Idempotency-Key': idempotencyKey }
    });
    duplicateOperationKeys.delete(id);
    storeDuplicateOperationKey(id, null);
    return data;
}
/**
 * Update account
 * @param id - Account ID
 * @param updates - Fields to update
 * @returns Updated account
 */
export async function update(id, updates) {
    const { data } = await apiClient.put(`/admin/accounts/${id}`, updates);
    return data;
}
export async function getGrokMediaEligibility(id) {
    const { data } = await apiClient.get(`/admin/accounts/${id}/grok-media-eligibility`);
    return data;
}
export async function updateGrokMediaEligibility(id, mode) {
    const { data } = await apiClient.put(`/admin/accounts/${id}/grok-media-eligibility`, { mode });
    return data;
}
/**
 * Check mixed-channel risk for account-group binding.
 */
export async function checkMixedChannelRisk(payload) {
    const { data } = await apiClient.post('/admin/accounts/check-mixed-channel', payload);
    return data;
}
/**
 * Delete account
 * @param id - Account ID
 * @returns Success confirmation
 */
export async function deleteAccount(id) {
    const { data } = await apiClient.delete(`/admin/accounts/${id}`);
    return data;
}
/**
 * Toggle account status
 * @param id - Account ID
 * @param status - New status
 * @returns Updated account
 */
export async function toggleStatus(id, status) {
    return update(id, { status });
}
/**
 * Test account connectivity
 * @param id - Account ID
 * @returns Test result
 */
export async function testAccount(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/test`);
    return data;
}
export async function batchTestAccounts(accountIds, modelId, onEvent) {
    const response = await fetch(buildApiUrl('/admin/accounts/batch-test'), {
        method: 'POST',
        headers: {
            Authorization: `Bearer ${localStorage.getItem('auth_token')}`,
            'Content-Type': 'application/json',
            [ADMIN_UI_REQUEST_HEADER]: '1'
        },
        credentials: 'include',
        body: JSON.stringify({ account_ids: accountIds, model_id: modelId })
    });
    if (!response.ok || !response.body) {
        throw new Error(`Batch account test failed (${response.status})`);
    }
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    while (true) {
        const { value, done } = await reader.read();
        buffer += decoder.decode(value || new Uint8Array(), { stream: !done });
        for (const block of buffer.split('\n\n').slice(0, -1)) {
            const line = block.split('\n').find(item => item.startsWith('data: '));
            if (!line)
                continue;
            try {
                onEvent(JSON.parse(line.slice(6)));
            }
            catch {
                continue;
            }
        }
        buffer = buffer.includes('\n\n') ? buffer.slice(buffer.lastIndexOf('\n\n') + 2) : buffer;
        if (done)
            break;
    }
}
export async function refreshCredentials(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/refresh`);
    return 'account' in data ? data : { account: data };
}
/**
 * Apply OAuth credentials after re-authorization.
 *
 * Unlike `update()`, this endpoint:
 * - never overwrites the whole `extra` JSONB (merges incrementally instead),
 *   so persistent settings like `base_rpm`, `window_cost_limit`, `max_sessions`,
 *   `quota_*` and `privacy_mode` are preserved
 * - clears the account error and invalidates the token cache server-side
 */
export async function applyOAuthCredentials(id, payload) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/apply-oauth-credentials`, payload);
    return data;
}
/**
 * Get account usage statistics
 * @param id - Account ID
 * @param days - Number of days (default: 30)
 * @returns Account usage statistics with history, summary, and models
 */
export async function getStats(id, days = 30) {
    const { data } = await apiClient.get(`/admin/accounts/${id}/stats`, {
        params: { days }
    });
    return data;
}
/**
 * Clear account error
 * @param id - Account ID
 * @returns Updated account
 */
export async function clearError(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/clear-error`);
    return data;
}
/**
 * Get account usage information (5h/7d window)
 * @param id - Account ID
 * @returns Account usage info
 */
export async function getUsage(id, source, force) {
    const params = {};
    if (source)
        params.source = source;
    if (force)
        params.force = 'true';
    const { data } = await apiClient.get(`/admin/accounts/${id}/usage`, {
        params: Object.keys(params).length > 0 ? params : undefined
    });
    return data;
}
export async function getBatchUsage(accountIds, force) {
    const { data } = await apiClient.post('/admin/accounts/usage/batch', {
        account_ids: accountIds,
        force: force === true
    });
    return data;
}
/**
 * Clear account rate limit status
 * @param id - Account ID
 * @returns Updated account
 */
export async function clearRateLimit(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/clear-rate-limit`);
    return data;
}
/**
 * Recover account runtime state in one call
 * @param id - Account ID
 * @returns Updated account
 */
export async function recoverState(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/recover-state`);
    return data;
}
/**
 * Reset account quota usage
 * @param id - Account ID
 * @returns Updated account
 */
export async function resetAccountQuota(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/reset-quota`);
    return data;
}
/**
 * Get temporary unschedulable status
 * @param id - Account ID
 * @returns Status with detail state if active
 */
export async function getTempUnschedulableStatus(id) {
    const { data } = await apiClient.get(`/admin/accounts/${id}/temp-unschedulable`);
    return data;
}
/**
 * Reset temporary unschedulable status
 * @param id - Account ID
 * @returns Success confirmation
 */
export async function resetTempUnschedulable(id) {
    const { data } = await apiClient.delete(`/admin/accounts/${id}/temp-unschedulable`);
    return data;
}
/**
 * Generate OAuth authorization URL
 * @param endpoint - API endpoint path
 * @param config - Proxy configuration
 * @returns Auth URL and session ID
 */
export async function generateAuthUrl(endpoint, config) {
    const { data } = await apiClient.post(endpoint, config);
    return data;
}
/**
 * Exchange authorization code for tokens
 * @param endpoint - API endpoint path
 * @param exchangeData - Session ID, code, and optional proxy config
 * @returns Token information
 */
export async function exchangeCode(endpoint, exchangeData) {
    const { data } = await apiClient.post(endpoint, exchangeData);
    return data;
}
/**
 * Batch create accounts
 * @param accounts - Array of account data
 * @returns Results of batch creation
 */
export async function batchCreate(accounts) {
    const { data } = await apiClient.post('/admin/accounts/batch', { accounts });
    return data;
}
/**
 * Batch update credentials fields for multiple accounts
 * @param request - Batch update request containing account IDs, field name, and value
 * @returns Results of batch update
 */
export async function batchUpdateCredentials(request) {
    const { data } = await apiClient.post('/admin/accounts/batch-update-credentials', request);
    return data;
}
/**
 * Bulk update multiple accounts
 * @param accountIds - Array of account IDs
 * @param updates - Fields to update
 * @returns Success confirmation
 */
export async function bulkUpdate(accountIdsOrPayload, updates) {
    const payload = Array.isArray(accountIdsOrPayload)
        ? {
            account_ids: accountIdsOrPayload,
            ...(updates ?? {})
        }
        : accountIdsOrPayload;
    const { data } = await apiClient.post('/admin/accounts/bulk-update', payload);
    return data;
}
/**
 * Get account today statistics
 * @param id - Account ID
 * @returns Today's stats (requests, tokens, cost)
 */
export async function getTodayStats(id) {
    const { data } = await apiClient.get(`/admin/accounts/${id}/today-stats`);
    return data;
}
/**
 * 批量获取多个账号的今日统计
 * @param accountIds - 账号 ID 列表
 * @returns 以账号 ID（字符串）为键的统计映射
 */
export async function getBatchTodayStats(accountIds) {
    const { data } = await apiClient.post('/admin/accounts/today-stats/batch', {
        account_ids: accountIds
    });
    return data;
}
/**
 * Set account schedulable status
 * @param id - Account ID
 * @param schedulable - Whether the account should participate in scheduling
 * @returns Updated account
 */
export async function setSchedulable(id, schedulable) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/schedulable`, {
        schedulable
    });
    return data;
}
/**
 * Get available models for an account
 * @param id - Account ID
 * @returns List of available models for this account
 */
export async function getAvailableModels(id) {
    const { data } = await apiClient.get(`/admin/accounts/${id}/models`);
    return data;
}
export async function getModelReasoning(id, modelId) {
    const { data } = await apiClient.get(`/admin/accounts/${id}/models/reasoning`, {
        params: { model_id: modelId }
    });
    return data;
}
/**
 * Sync live supported models from the account's upstream model-list endpoint
 * @param id - Account ID
 * @returns List of model IDs returned by the upstream
 */
export async function syncUpstreamModels(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/models/sync-upstream`);
    return data;
}
/**
 * Preview upstream models without a saved account (create-flow)
 * @param params - Connection credentials
 * @returns List of model IDs returned by the upstream
 */
export async function syncUpstreamModelsPreview(params) {
    const { data } = await apiClient.post('/admin/accounts/models/sync-upstream-preview', params);
    return data;
}
export async function previewFromCrs(params) {
    const { data } = await apiClient.post('/admin/accounts/sync/crs/preview', params);
    return data;
}
export async function syncFromCrs(params) {
    const { data } = await apiClient.post('/admin/accounts/sync/crs', params, {
        timeout: 180000 // 180s timeout: sync refreshes each existing account's OAuth token serially
    });
    return data;
}
export async function exportData(options) {
    const params = {};
    if (options?.ids && options.ids.length > 0) {
        params.ids = options.ids.join(',');
    }
    else if (options?.filters) {
        const { platform, type, status, group, privacy_mode, search, sort_by, sort_order } = options.filters;
        if (platform)
            params.platform = platform;
        if (type)
            params.type = type;
        if (status)
            params.status = status;
        if (group)
            params.group = group;
        if (privacy_mode)
            params.privacy_mode = privacy_mode;
        if (search)
            params.search = search;
        if (sort_by)
            params.sort_by = sort_by;
        if (sort_order)
            params.sort_order = sort_order;
    }
    if (options?.includeProxies === false) {
        params.include_proxies = 'false';
    }
    const { data } = await apiClient.get('/admin/accounts/data', { params });
    return data;
}
export async function importData(payload) {
    const { data } = await apiClient.post('/admin/accounts/data', {
        data: payload.data,
        group_ids: payload.group_ids,
        skip_default_group_bind: payload.skip_default_group_bind
    });
    return data;
}
export async function importCodexSession(payload) {
    const { data } = await apiClient.post('/admin/accounts/import/codex-session', payload, {
        timeout: 120000 // 120s timeout for large session imports
    });
    return data;
}
export async function createOpenAICodexPAT(payload) {
    const { data } = await apiClient.post('/admin/openai/create-from-codex-pat', payload);
    return data;
}
/**
 * Get Antigravity default model mapping from backend
 * @returns Default model mapping (from -> to)
 */
export async function getAntigravityDefaultModelMapping() {
    const { data } = await apiClient.get('/admin/accounts/antigravity/default-model-mapping');
    return data;
}
/**
 * Refresh OpenAI token using refresh token
 * @param refreshToken - The refresh token
 * @param proxyId - Optional proxy ID
 * @returns Token information including access_token, email, etc.
 */
export async function refreshOpenAIToken(refreshToken, proxyId, endpoint = '/admin/openai/refresh-token', clientId) {
    const payload = {
        refresh_token: refreshToken
    };
    if (proxyId) {
        payload.proxy_id = proxyId;
    }
    if (clientId) {
        payload.client_id = clientId;
    }
    const { data } = await apiClient.post(endpoint, payload);
    return data;
}
/**
 * Revert account proxy to original before fallback
 * @param id - Account ID
 * @returns Success confirmation
 */
export async function revertProxyFallback(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/revert-proxy-fallback`);
    return data;
}
/**
 * Delete multiple accounts with bounded server-side concurrency.
 */
export async function batchDelete(accountIds) {
    const { data } = await apiClient.post('/admin/accounts/batch-delete', {
        account_ids: accountIds
    });
    return data;
}
/**
 * Batch clear account errors
 * @param accountIds - Array of account IDs
 * @returns Batch operation result
 */
export async function batchClearError(accountIds) {
    const { data } = await apiClient.post('/admin/accounts/batch-clear-error', {
        account_ids: accountIds
    });
    return data;
}
/**
 * Batch refresh account credentials
 * @param accountIds - Array of account IDs
 * @returns Batch operation result
 */
export async function batchRefresh(accountIds) {
    const { data } = await apiClient.post('/admin/accounts/batch-refresh', {
        account_ids: accountIds,
    }, {
        timeout: 120000 // 120s timeout for large batch refreshes
    });
    return data;
}
/**
 * Set privacy for an Antigravity OAuth account
 * @param id - Account ID
 * @returns Updated account
 */
export async function setPrivacy(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/set-privacy`);
    return data;
}
/**
 * Query the upstream quota AND persist the reset-credit snapshot on the account
 * so the card can be rehydrated without an upstream round-trip. It is a POST
 * because it writes account state (and must therefore be audited).
 *
 * The read-only `GET /admin/openai/accounts/:id/quota` endpoint still exists for
 * API consumers; the panel always wants the snapshot persisted, so it has no
 * client binding here.
 */
export async function refreshOpenAIQuota(id) {
    const { data } = await apiClient.post(`/admin/openai/accounts/${id}/quota/refresh`);
    return data;
}
export async function refreshOpenAIReferrals(id) {
    const { data } = await apiClient.post(`/admin/openai/accounts/${id}/referrals/refresh`);
    return data;
}
export async function sendOpenAIReferralInvite(id, input) {
    const { data } = await apiClient.post(`/admin/openai/accounts/${id}/referrals/invite`, input, { timeout: 90000 });
    return data;
}
/**
 * Consume one rate-limit-reset credit for an OpenAI/Codex OAuth account.
 *
 * The credit is non-refundable and the endpoint chains an upstream reset with an
 * upstream re-query, so it needs a larger budget than the default client
 * timeout: aborting locally would report a successful consumption as a failure
 * and invite a retry that spends a second credit.
 */
export async function resetOpenAIQuota(id) {
    const { data } = await apiClient.post(`/admin/openai/accounts/${id}/reset-quota`, undefined, { timeout: 90000 });
    return data;
}
export async function createSparkShadow(parentId, payload) {
    const { data } = await apiClient.post(`/admin/accounts/${parentId}/shadow`, payload);
    return data;
}
export async function getNewAPIUpstreamConfig(id) {
    const { data } = await apiClient.get(`/admin/accounts/${id}/upstream-billing-probe/config`);
    return data;
}
export async function previewNewAPIUpstreamConfig(id, request) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/upstream-billing-probe/config/preview`, request);
    return data;
}
export async function saveNewAPIUpstreamConfig(id, request) {
    const { data } = await apiClient.put(`/admin/accounts/${id}/upstream-billing-probe/config`, request);
    return data;
}
export async function deleteNewAPIUpstreamConfig(id) {
    const { data } = await apiClient.delete(`/admin/accounts/${id}/upstream-billing-probe/config`);
    return data;
}
export async function getUpstreamBillingProbeSettings() {
    const { data } = await apiClient.get('/admin/accounts/upstream-billing-probe/settings');
    return data;
}
export async function updateUpstreamBillingProbeSettings(settings) {
    const { data } = await apiClient.put('/admin/accounts/upstream-billing-probe/settings', settings);
    return data;
}
export async function setUpstreamBillingProbeEnabled(id, enabled) {
    await apiClient.put(`/admin/accounts/${id}/upstream-billing-probe`, { enabled });
}
export async function probeUpstreamBilling(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/upstream-billing-probe`);
    return data;
}
export async function probeUpstreamBillingBatch(accountIds) {
    const { data } = await apiClient.post('/admin/accounts/upstream-billing-probe/batch', { account_ids: accountIds });
    return data.results;
}
export async function probeUpstreamUsage(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/upstream-usage-probe`);
    return data;
}
export async function probeUpstreamUsageBatch(accountIds) {
    const { data } = await apiClient.post('/admin/accounts/upstream-usage-probe/batch', { account_ids: accountIds });
    return data.results;
}
/**
 * Two-shot Codex turn-state probe: mint a ticket, then continue with it. A new
 * ticket on the continuation means the account is degraded. Each shot may take
 * up to 45s upstream, so the default client timeout is too short.
 */
export async function probeOpenAICodexState(id, modelId, options) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/state-probe`, { model_id: modelId?.trim() || undefined }, { timeout: 120000, signal: options?.signal });
    return data;
}
export async function getOllamaCloudUsageSettings() {
    const { data } = await apiClient.get('/admin/accounts/ollama-cloud-usage/settings');
    return data;
}
export async function updateOllamaCloudUsageSettings(settings) {
    const { data } = await apiClient.put('/admin/accounts/ollama-cloud-usage/settings', settings);
    return data;
}
export async function getOllamaCloudUsage(id) {
    const { data } = await apiClient.get(`/admin/accounts/${id}/ollama-cloud-usage`);
    return data;
}
export async function saveOllamaCloudUsageSession(id, session) {
    const { data } = await apiClient.put(`/admin/accounts/${id}/ollama-cloud-usage/session`, {
        session
    });
    return data;
}
export async function deleteOllamaCloudUsageSession(id) {
    const { data } = await apiClient.delete(`/admin/accounts/${id}/ollama-cloud-usage/session`);
    return data;
}
export async function setOllamaCloudUsageAutoRefresh(id, enabled) {
    const { data } = await apiClient.put(`/admin/accounts/${id}/ollama-cloud-usage/auto-refresh`, {
        enabled
    });
    return data;
}
export async function refreshOllamaCloudUsage(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/ollama-cloud-usage/refresh`);
    return data;
}
export async function getCodexHarvestFlow() {
    const { data } = await apiClient.get('/admin/accounts/codex-harvest-flow');
    return data;
}
export async function updateCodexSkipHarvest(id, skipHarvest) {
    const { data } = await apiClient.put(`/admin/accounts/${id}/codex-skip-harvest`, {
        skip_harvest: skipHarvest
    });
    return data;
}
export async function getOpenCodeGoUsageSettings() {
    const { data } = await apiClient.get('/admin/accounts/opencode-go-usage/settings');
    return data;
}
export async function updateOpenCodeGoUsageSettings(settings) {
    const { data } = await apiClient.put('/admin/accounts/opencode-go-usage/settings', settings);
    return data;
}
export async function getOpenCodeGoUsage(id) {
    const { data } = await apiClient.get(`/admin/accounts/${id}/opencode-go-usage`);
    return data;
}
export async function setOpenCodeGoUsageAutoRefresh(id, enabled) {
    const { data } = await apiClient.put(`/admin/accounts/${id}/opencode-go-usage/auto-refresh`, {
        enabled
    });
    return data;
}
export async function streamManualCodexHarvest(accountId, body, onProgress, signal) {
    const headers = {
        'Content-Type': 'application/json',
        Accept: 'text/event-stream',
        [ADMIN_UI_REQUEST_HEADER]: '1'
    };
    const token = localStorage.getItem('auth_token');
    if (token)
        headers.Authorization = `Bearer ${token}`;
    const res = await fetch(buildApiUrl(`/admin/accounts/${accountId}/manual-harvest`), {
        method: 'POST',
        credentials: 'include',
        headers,
        body: JSON.stringify(body),
        signal
    });
    if (res.status === 401) {
        const error = new Error('HTTP 401');
        error.status = 401;
        throw error;
    }
    if (!res.ok || !res.body) {
        let detail = '';
        try {
            detail = (await res.text()).slice(0, 200);
        }
        catch {
            detail = '';
        }
        throw new Error(detail ? `HTTP ${res.status} - ${detail}` : `HTTP ${res.status}`);
    }
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    while (true) {
        const { done, value } = await reader.read();
        if (done)
            break;
        buffer += decoder.decode(value, { stream: true });
        const frames = buffer.split('\n\n');
        buffer = frames.pop() || '';
        for (const frame of frames) {
            if (!frame.startsWith('data: '))
                continue;
            try {
                onProgress(JSON.parse(frame.slice(6)));
            }
            catch {
                // ignore a malformed SSE frame
            }
        }
    }
    if (buffer.startsWith('data: ')) {
        try {
            onProgress(JSON.parse(buffer.slice(6)));
        }
        catch {
            // ignore a trailing malformed SSE frame
        }
    }
}
export async function refreshOpenCodeGoUsage(id) {
    const { data } = await apiClient.post(`/admin/accounts/${id}/opencode-go-usage/refresh`);
    return data;
}
export async function getOpenAIRequestTimezones() {
    const { data } = await apiClient.get('/admin/accounts/openai-request-timezones');
    return data;
}
export const accountsAPI = {
    getOpenAIRequestTimezones,
    list,
    listWithEtag,
    getUpstreamBillingRatesWithEtag,
    getById,
    create,
    duplicate,
    update,
    getGrokMediaEligibility,
    updateGrokMediaEligibility,
    checkMixedChannelRisk,
    delete: deleteAccount,
    toggleStatus,
    testAccount,
    batchTestAccounts,
    refreshCredentials,
    applyOAuthCredentials,
    getStats,
    clearError,
    getUsage,
    getBatchUsage,
    getTodayStats,
    getBatchTodayStats,
    clearRateLimit,
    recoverState,
    resetAccountQuota,
    getTempUnschedulableStatus,
    resetTempUnschedulable,
    setSchedulable,
    getAvailableModels,
    getModelReasoning,
    syncUpstreamModels,
    syncUpstreamModelsPreview,
    generateAuthUrl,
    exchangeCode,
    refreshOpenAIToken,
    batchCreate,
    batchUpdateCredentials,
    bulkUpdate,
    previewFromCrs,
    syncFromCrs,
    exportData,
    importData,
    getManagementCapabilities,
    importCodexSession,
    createOpenAICodexPAT,
    getAntigravityDefaultModelMapping,
    batchDelete,
    batchClearError,
    batchRefresh,
    setPrivacy,
    revertProxyFallback,
    refreshOpenAIQuota,
    resetOpenAIQuota,
    createSparkShadow,
    getNewAPIUpstreamConfig,
    previewNewAPIUpstreamConfig,
    saveNewAPIUpstreamConfig,
    deleteNewAPIUpstreamConfig,
    getUpstreamBillingProbeSettings,
    updateUpstreamBillingProbeSettings,
    setUpstreamBillingProbeEnabled,
    probeUpstreamBilling,
    probeUpstreamBillingBatch,
    probeUpstreamUsage,
    probeUpstreamUsageBatch,
    probeOpenAICodexState,
    getOllamaCloudUsageSettings,
    updateOllamaCloudUsageSettings,
    getOllamaCloudUsage,
    saveOllamaCloudUsageSession,
    deleteOllamaCloudUsageSession,
    setOllamaCloudUsageAutoRefresh,
    refreshOllamaCloudUsage,
    getCodexHarvestFlow,
    updateCodexSkipHarvest,
    streamManualCodexHarvest,
    getOpenCodeGoUsageSettings,
    updateOpenCodeGoUsageSettings,
    getOpenCodeGoUsage,
    setOpenCodeGoUsageAutoRefresh,
    refreshOpenCodeGoUsage
};
export default accountsAPI;
export async function getManagementCapabilities() {
    const { data } = await apiClient.get('/admin/accounts/management-capabilities');
    return data;
}
