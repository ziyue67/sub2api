/**
 * Pelican group tests: the scheduled questions that feed the user Pelican showcase.
 * The gateway scheduler picks the answering account, exactly as for a user request.
 */
import { apiClient } from '../client';
export async function listPlans() {
    const { data } = await apiClient.get('/admin/pelican-group-tests');
    return data ?? [];
}
export async function createPlan(input) {
    const { data } = await apiClient.post('/admin/pelican-group-tests', input);
    return data;
}
export async function updatePlan(id, input) {
    const { data } = await apiClient.put(`/admin/pelican-group-tests/${id}`, input);
    return data;
}
export async function deletePlan(id) {
    await apiClient.delete(`/admin/pelican-group-tests/${id}`);
}
/** Starts a run in the background; rejects with PELICAN_GROUP_TEST_PLAN_RUNNING while one is in progress. */
export async function runPlan(id) {
    await apiClient.post(`/admin/pelican-group-tests/${id}/run`);
}
/** Newest first, without HTML; only the requested page is transferred. */
export async function listResults(page = 1, pageSize = 20, planId = 0, signal) {
    const { data } = await apiClient.get('/admin/pelican-group-test-results', {
        params: { page, page_size: pageSize, plan_id: planId || undefined },
        signal,
    });
    return data;
}
export async function getResult(id) {
    const { data } = await apiClient.get(`/admin/pelican-group-test-results/${id}`);
    return data;
}
export async function getShowcaseSettings() {
    const { data } = await apiClient.get('/admin/pelican-showcase/settings');
    return data;
}
export async function updateShowcaseSettings(settings) {
    const { data } = await apiClient.put('/admin/pelican-showcase/settings', settings);
    return data;
}
export const pelicanTestsAPI = {
    listPlans,
    createPlan,
    updatePlan,
    deletePlan,
    runPlan,
    listResults,
    getResult,
    getShowcaseSettings,
    updateShowcaseSettings,
};
export default pelicanTestsAPI;
