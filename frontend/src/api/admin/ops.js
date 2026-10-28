/**
 * Admin Ops API endpoints (vNext)
 * - Error logs list/detail
 * - Dashboard overview (raw path)
 */
import { apiClient, buildGatewayUrl } from '../client';
export async function getConcurrencyStats(platform, groupId) {
    const params = {};
    if (platform) {
        params.platform = platform;
    }
    if (typeof groupId === 'number' && groupId > 0) {
        params.group_id = groupId;
    }
    const { data } = await apiClient.get('/admin/ops/concurrency', { params });
    return data;
}
export async function getUserConcurrencyStats() {
    const { data } = await apiClient.get('/admin/ops/user-concurrency');
    return data;
}
export async function getAccountAvailabilityStats(platform, groupId) {
    const params = {};
    if (platform) {
        params.platform = platform;
    }
    if (typeof groupId === 'number' && groupId > 0) {
        params.group_id = groupId;
    }
    const { data } = await apiClient.get('/admin/ops/account-availability', { params });
    return data;
}
export async function getRealtimeTrafficSummary(window, platform, groupId) {
    const params = { window };
    if (platform) {
        params.platform = platform;
    }
    if (typeof groupId === 'number' && groupId > 0) {
        params.group_id = groupId;
    }
    const { data } = await apiClient.get('/admin/ops/realtime-traffic', { params });
    return data;
}
export const OPS_WS_CLOSE_CODES = {
    REALTIME_DISABLED: 4001
};
const OPS_WS_BASE_PROTOCOL = 'sub2api-admin';
export function subscribeQPS(onMessage, options = {}) {
    let ws = null;
    let reconnectAttempts = 0;
    const maxReconnectAttempts = Number.isFinite(options.maxReconnectAttempts)
        ? options.maxReconnectAttempts
        : Infinity;
    const baseDelayMs = options.reconnectBaseDelayMs ?? 1000;
    const maxDelayMs = options.reconnectMaxDelayMs ?? 30000;
    let reconnectTimer = null;
    let shouldReconnect = true;
    let isConnecting = false;
    let hasConnectedOnce = false;
    let lastMessageAt = 0;
    const staleTimeoutMs = options.staleTimeoutMs ?? 120000;
    const staleCheckIntervalMs = options.staleCheckIntervalMs ?? 30000;
    let staleTimer = null;
    const setStatus = (status) => {
        options.onStatusChange?.(status);
    };
    const clearReconnectTimer = () => {
        if (reconnectTimer) {
            clearTimeout(reconnectTimer);
            reconnectTimer = null;
        }
    };
    const clearStaleTimer = () => {
        if (staleTimer) {
            clearInterval(staleTimer);
            staleTimer = null;
        }
    };
    const startStaleTimer = () => {
        clearStaleTimer();
        if (!staleTimeoutMs || staleTimeoutMs <= 0)
            return;
        staleTimer = setInterval(() => {
            if (!shouldReconnect)
                return;
            if (!ws || ws.readyState !== WebSocket.OPEN)
                return;
            if (!lastMessageAt)
                return;
            const ageMs = Date.now() - lastMessageAt;
            if (ageMs > staleTimeoutMs) {
                // Treat as a half-open connection; closing triggers the normal reconnect path.
                ws.close();
            }
        }, staleCheckIntervalMs);
    };
    const scheduleReconnect = () => {
        if (!shouldReconnect)
            return;
        if (hasConnectedOnce && reconnectAttempts >= maxReconnectAttempts)
            return;
        // If we're offline, wait for the browser to come back online.
        if (typeof navigator !== 'undefined' && 'onLine' in navigator && !navigator.onLine) {
            setStatus('offline');
            return;
        }
        const expDelay = baseDelayMs * Math.pow(2, reconnectAttempts);
        const delay = Math.min(expDelay, maxDelayMs);
        const jitter = Math.floor(Math.random() * 250);
        clearReconnectTimer();
        reconnectTimer = setTimeout(() => {
            reconnectAttempts++;
            connect();
        }, delay + jitter);
        options.onReconnectScheduled?.({ attempt: reconnectAttempts + 1, delayMs: delay + jitter });
    };
    const handleOnline = () => {
        if (!shouldReconnect)
            return;
        if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING))
            return;
        connect();
    };
    const handleOffline = () => {
        setStatus('offline');
    };
    const connect = () => {
        if (!shouldReconnect)
            return;
        if (isConnecting)
            return;
        if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING))
            return;
        if (hasConnectedOnce && reconnectAttempts >= maxReconnectAttempts)
            return;
        isConnecting = true;
        setStatus(hasConnectedOnce ? 'reconnecting' : 'connecting');
        const wsBaseUrl = options.wsBaseUrl || import.meta.env.VITE_WS_BASE_URL;
        const wsURL = wsBaseUrl
            ? new URL(`${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${wsBaseUrl}/api/v1/admin/ops/ws/qps`)
            : new URL(buildGatewayUrl('/api/v1/admin/ops/ws/qps').replace(/^http/, 'ws'));
        // Do NOT put admin JWT in the URL query string (it can leak via access logs, proxies, etc).
        // Browsers cannot set Authorization headers for WebSockets, so we pass the token via
        // Sec-WebSocket-Protocol (subprotocol list): ["sub2api-admin", "jwt.<token>"].
        const rawToken = String(options.token ?? localStorage.getItem('auth_token') ?? '').trim();
        const protocols = [OPS_WS_BASE_PROTOCOL];
        if (rawToken)
            protocols.push(`jwt.${rawToken}`);
        ws = new WebSocket(wsURL.toString(), protocols);
        ws.onopen = () => {
            reconnectAttempts = 0;
            isConnecting = false;
            hasConnectedOnce = true;
            clearReconnectTimer();
            lastMessageAt = Date.now();
            startStaleTimer();
            setStatus('connected');
            options.onOpen?.();
        };
        ws.onmessage = (e) => {
            try {
                const data = JSON.parse(e.data);
                lastMessageAt = Date.now();
                onMessage(data);
            }
            catch (err) {
                console.warn('[OpsWS] Failed to parse message:', err);
            }
        };
        ws.onerror = (error) => {
            console.error('[OpsWS] Connection error:', error);
            options.onError?.(error);
        };
        ws.onclose = (event) => {
            isConnecting = false;
            options.onClose?.(event);
            clearStaleTimer();
            ws = null;
            // If the server explicitly tells us to stop reconnecting, honor it.
            if (event && typeof event.code === 'number' && event.code === OPS_WS_CLOSE_CODES.REALTIME_DISABLED) {
                shouldReconnect = false;
                clearReconnectTimer();
                setStatus('closed');
                options.onFatalClose?.(event);
                return;
            }
            scheduleReconnect();
        };
    };
    window.addEventListener('online', handleOnline);
    window.addEventListener('offline', handleOffline);
    connect();
    return () => {
        shouldReconnect = false;
        window.removeEventListener('online', handleOnline);
        window.removeEventListener('offline', handleOffline);
        clearReconnectTimer();
        clearStaleTimer();
        if (ws)
            ws.close();
        ws = null;
        setStatus('closed');
    };
}
export async function getDashboardOverview(params, options = {}) {
    const { data } = await apiClient.get('/admin/ops/dashboard/overview', {
        params,
        signal: options.signal
    });
    return data;
}
export async function getDashboardSnapshotV2(params, options = {}) {
    const { data } = await apiClient.get('/admin/ops/dashboard/snapshot-v2', {
        params,
        signal: options.signal
    });
    return data;
}
export async function getThroughputTrend(params, options = {}) {
    const { data } = await apiClient.get('/admin/ops/dashboard/throughput-trend', {
        params,
        signal: options.signal
    });
    return data;
}
export async function getLatencyHistogram(params, options = {}) {
    const { data } = await apiClient.get('/admin/ops/dashboard/latency-histogram', {
        params,
        signal: options.signal
    });
    return data;
}
export async function getErrorTrend(params, options = {}) {
    const { data } = await apiClient.get('/admin/ops/dashboard/error-trend', {
        params,
        signal: options.signal
    });
    return data;
}
export async function getErrorDistribution(params, options = {}) {
    const { data } = await apiClient.get('/admin/ops/dashboard/error-distribution', {
        params,
        signal: options.signal
    });
    return data;
}
export async function getOpenAITokenStats(params, options = {}) {
    const { data } = await apiClient.get('/admin/ops/dashboard/openai-token-stats', {
        params,
        signal: options.signal
    });
    return data;
}
// Legacy unified endpoints
export async function listErrorLogs(params) {
    const { data } = await apiClient.get('/admin/ops/errors', { params });
    return data;
}
export async function getErrorLogDetail(id) {
    const { data } = await apiClient.get(`/admin/ops/errors/${id}`);
    return data;
}
export async function updateErrorResolved(errorId, resolved) {
    await apiClient.put(`/admin/ops/errors/${errorId}/resolve`, { resolved });
}
// New split endpoints
export async function listRequestErrors(params) {
    const { data } = await apiClient.get('/admin/ops/request-errors', { params });
    return data;
}
export async function listUpstreamErrors(params) {
    const { data } = await apiClient.get('/admin/ops/upstream-errors', { params });
    return data;
}
export async function getRequestErrorDetail(id) {
    const { data } = await apiClient.get(`/admin/ops/request-errors/${id}`);
    return data;
}
export async function getUpstreamErrorDetail(id) {
    const { data } = await apiClient.get(`/admin/ops/upstream-errors/${id}`);
    return data;
}
export async function updateRequestErrorResolved(errorId, resolved) {
    await apiClient.put(`/admin/ops/request-errors/${errorId}/resolve`, { resolved });
}
export async function updateUpstreamErrorResolved(errorId, resolved) {
    await apiClient.put(`/admin/ops/upstream-errors/${errorId}/resolve`, { resolved });
}
export async function listRequestErrorUpstreamErrors(id, params = {}, options = {}) {
    const query = { ...params };
    if (options.include_detail)
        query.include_detail = '1';
    const { data } = await apiClient.get(`/admin/ops/request-errors/${id}/upstream-errors`, { params: query });
    return data;
}
export async function listRequestDetails(params) {
    const { data } = await apiClient.get('/admin/ops/requests', { params });
    return data;
}
// Alert rules
export async function listAlertRules() {
    const { data } = await apiClient.get('/admin/ops/alert-rules');
    return data;
}
export async function createAlertRule(rule) {
    const { data } = await apiClient.post('/admin/ops/alert-rules', rule);
    return data;
}
export async function updateAlertRule(id, rule) {
    const { data } = await apiClient.put(`/admin/ops/alert-rules/${id}`, rule);
    return data;
}
export async function deleteAlertRule(id) {
    await apiClient.delete(`/admin/ops/alert-rules/${id}`);
}
export async function listAlertEvents(params = {}) {
    const { data } = await apiClient.get('/admin/ops/alert-events', { params });
    return data;
}
export async function getAlertEvent(id) {
    const { data } = await apiClient.get(`/admin/ops/alert-events/${id}`);
    return data;
}
export async function updateAlertEventStatus(id, status) {
    await apiClient.put(`/admin/ops/alert-events/${id}/status`, { status });
}
export async function createAlertSilence(payload) {
    await apiClient.post('/admin/ops/alert-silences', payload);
}
// Email notification config
export async function getEmailNotificationConfig() {
    const { data } = await apiClient.get('/admin/ops/email-notification/config');
    return data;
}
export async function updateEmailNotificationConfig(config) {
    const { data } = await apiClient.put('/admin/ops/email-notification/config', config);
    return data;
}
// Runtime settings (DB-backed)
export async function getAlertRuntimeSettings() {
    const { data } = await apiClient.get('/admin/ops/runtime/alert');
    return data;
}
export async function updateAlertRuntimeSettings(config) {
    const { data } = await apiClient.put('/admin/ops/runtime/alert', config);
    return data;
}
export async function getRuntimeLogConfig() {
    const { data } = await apiClient.get('/admin/ops/runtime/logging');
    return data;
}
export async function updateRuntimeLogConfig(config) {
    const { data } = await apiClient.put('/admin/ops/runtime/logging', config);
    return data;
}
export async function resetRuntimeLogConfig() {
    const { data } = await apiClient.post('/admin/ops/runtime/logging/reset');
    return data;
}
export async function listSystemLogs(params) {
    const { data } = await apiClient.get('/admin/ops/system-logs', { params });
    return data;
}
export async function cleanupSystemLogs(payload) {
    const { data } = await apiClient.post('/admin/ops/system-logs/cleanup', payload);
    return data;
}
export async function getSystemLogSinkHealth() {
    const { data } = await apiClient.get('/admin/ops/system-logs/health');
    return data;
}
// Advanced settings (DB-backed)
export async function getAdvancedSettings() {
    const { data } = await apiClient.get('/admin/ops/advanced-settings');
    return data;
}
export async function updateAdvancedSettings(config) {
    const { data } = await apiClient.put('/admin/ops/advanced-settings', config);
    return data;
}
// ==================== Metric Thresholds ====================
async function getMetricThresholds() {
    const { data } = await apiClient.get('/admin/ops/settings/metric-thresholds');
    return data;
}
async function updateMetricThresholds(thresholds) {
    await apiClient.put('/admin/ops/settings/metric-thresholds', thresholds);
}
export const opsAPI = {
    getDashboardSnapshotV2,
    getDashboardOverview,
    getThroughputTrend,
    getLatencyHistogram,
    getErrorTrend,
    getErrorDistribution,
    getOpenAITokenStats,
    getConcurrencyStats,
    getUserConcurrencyStats,
    getAccountAvailabilityStats,
    getRealtimeTrafficSummary,
    subscribeQPS,
    // Legacy unified endpoints
    listErrorLogs,
    getErrorLogDetail,
    updateErrorResolved,
    // New split endpoints
    listRequestErrors,
    listUpstreamErrors,
    getRequestErrorDetail,
    getUpstreamErrorDetail,
    updateRequestErrorResolved,
    updateUpstreamErrorResolved,
    listRequestErrorUpstreamErrors,
    listRequestDetails,
    listAlertRules,
    createAlertRule,
    updateAlertRule,
    deleteAlertRule,
    listAlertEvents,
    getAlertEvent,
    updateAlertEventStatus,
    createAlertSilence,
    getEmailNotificationConfig,
    updateEmailNotificationConfig,
    getAlertRuntimeSettings,
    updateAlertRuntimeSettings,
    getRuntimeLogConfig,
    updateRuntimeLogConfig,
    resetRuntimeLogConfig,
    getAdvancedSettings,
    updateAdvancedSettings,
    getMetricThresholds,
    updateMetricThresholds,
    listSystemLogs,
    cleanupSystemLogs,
    getSystemLogSinkHealth
};
export default opsAPI;
