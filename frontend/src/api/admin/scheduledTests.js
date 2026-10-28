/**
 * Admin Scheduled Tests API endpoints
 * Handles scheduled test plan management for account connectivity monitoring
 */
import { apiClient } from '../client';
/**
 * List all scheduled test plans for an account
 * @param accountId - Account ID
 * @returns List of scheduled test plans
 */
export async function listByAccount(accountId) {
    const { data } = await apiClient.get(`/admin/accounts/${accountId}/scheduled-test-plans`);
    return data ?? [];
}
/**
 * Create a new scheduled test plan
 * @param req - Plan creation request
 * @returns Created plan
 */
export async function create(req) {
    const { data } = await apiClient.post('/admin/scheduled-test-plans', req);
    return data;
}
/**
 * Update an existing scheduled test plan
 * @param id - Plan ID
 * @param req - Fields to update
 * @returns Updated plan
 */
export async function update(id, req) {
    const { data } = await apiClient.put(`/admin/scheduled-test-plans/${id}`, req);
    return data;
}
/**
 * Delete a scheduled test plan
 * @param id - Plan ID
 */
export async function deletePlan(id, ownership) {
    if (ownership)
        await apiClient.delete(`/admin/scheduled-test-plans/${id}`, { data: ownership });
    else
        await apiClient.delete(`/admin/scheduled-test-plans/${id}`);
}
/**
 * List test results for a plan
 * @param planId - Plan ID
 * @param limit - Optional max number of results to return
 * @returns List of test results
 */
export async function listResults(planId, limit, includeContent = true) {
    const { data } = await apiClient.get(`/admin/scheduled-test-plans/${planId}/results`, {
        params: { limit, include_content: includeContent }
    });
    return data ?? [];
}
export async function getResult(planId, resultId) {
    const { data } = await apiClient.get(`/admin/scheduled-test-plans/${planId}/results/${resultId}`);
    return data;
}
// accountId narrows the history to one account; omit it for every account.
export async function listPelicanHistory(beforeId = 0, accountId) {
    const params = accountId ? { before_id: beforeId, account_id: accountId } : { before_id: beforeId };
    const { data } = await apiClient.get('/admin/pelican-test-results', { params });
    return data;
}
export const scheduledTestsAPI = {
    listPelicanHistory,
    getResult,
    listByAccount,
    create,
    update,
    delete: deletePlan,
    listResults
};
export default scheduledTestsAPI;
