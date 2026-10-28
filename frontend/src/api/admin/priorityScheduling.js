import { apiClient } from '../client';
export async function getPriorityConfig() {
    return (await apiClient.get('/admin/priority-scheduling/config')).data;
}
export async function savePriorityConfig(config) {
    return (await apiClient.put('/admin/priority-scheduling/config', config)).data;
}
export async function getPrioritySnapshot() {
    return (await apiClient.get('/admin/priority-scheduling/snapshot')).data.snapshot;
}
