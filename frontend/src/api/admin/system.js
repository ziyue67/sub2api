/**
 * System API endpoints for admin operations
 */
import { apiClient } from '../client';
/**
 * Get current version
 */
export async function getVersion() {
    const { data } = await apiClient.get('/admin/system/version');
    return data;
}
/**
 * Check for updates
 * @param force - Force refresh from GitHub API
 */
export async function checkUpdates(force = false) {
    const { data } = await apiClient.get('/admin/system/check-updates', {
        params: force ? { force: 'true' } : undefined
    });
    return data;
}
/**
 * Get versions available for rollback (up to 3 versions older than current)
 */
export async function getRollbackVersions() {
    const { data } = await apiClient.get('/admin/system/rollback-versions');
    return data;
}
/**
 * In-place update/rollback downloads a full release binary from GitHub, which
 * can take several minutes on slow links. The global 30s axios timeout would
 * abort the request mid-download (#4504), so these calls wait as long as the
 * backend allows (15 minutes server-side).
 */
const UPDATE_REQUEST_TIMEOUT_MS = 15 * 60 * 1000;
/**
 * Perform system update
 * Downloads and applies the latest version
 */
export async function performUpdate() {
    const { data } = await apiClient.post('/admin/system/update', undefined, {
        timeout: UPDATE_REQUEST_TIMEOUT_MS
    });
    return data;
}
/**
 * Rollback to a previous version
 * @param version - Target version (e.g. "0.1.146"); omit to restore the local backup binary
 */
export async function rollback(version) {
    const { data } = await apiClient.post('/admin/system/rollback', version ? { version } : undefined, { timeout: UPDATE_REQUEST_TIMEOUT_MS });
    return data;
}
/**
 * Restart the service
 */
export async function restartService() {
    const { data } = await apiClient.post('/admin/system/restart');
    return data;
}
export const systemAPI = {
    getVersion,
    checkUpdates,
    performUpdate,
    getRollbackVersions,
    rollback,
    restartService
};
export default systemAPI;
