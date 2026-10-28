import { apiClient } from '../client';
const base = '/admin/risk-control/safety-prompt';
export async function getSafetyPromptConfig() {
    return (await apiClient.get(`${base}/config`)).data;
}
export async function updateSafetyPromptConfig(config) {
    return (await apiClient.put(`${base}/config`, config)).data;
}
export async function listSafetyPromptEvents(params) {
    return (await apiClient.get(`${base}/events`, { params })).data;
}
