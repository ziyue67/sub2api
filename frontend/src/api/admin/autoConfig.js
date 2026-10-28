import { list as listGroups } from './groups';
import { apiClient } from '../client';
export async function getAutoConfig() { return (await apiClient.get('/admin/account-ops/auto-config')).data; }
export async function saveAutoConfig(config) { return (await apiClient.put('/admin/account-ops/auto-config', config)).data; }
export async function getAutoConfigEvents(params = {}) {
    return (await apiClient.get('/admin/account-ops/auto-config/events', { params })).data;
}
// Collect every page before exposing scope options. A partial list must never
// turn selected IDs into an empty (global) scope.
export async function getModelBillingGroups() {
    const groups = new Map();
    for (let page = 1;; page++) {
        const batch = await listGroups(page, 100, { status: 'active', sort_by: 'id', sort_order: 'asc' });
        if (batch.page !== page || !Number.isSafeInteger(batch.pages) || batch.pages < 0 ||
            !Number.isSafeInteger(batch.total) || batch.total < 0)
            throw new Error('Incomplete group list');
        for (const group of batch.items)
            groups.set(group.id, group);
        if (page >= batch.pages) {
            if (groups.size < batch.total)
                throw new Error('Incomplete group list');
            return [...groups.values()];
        }
        if (!batch.items.length)
            throw new Error('Incomplete group list');
    }
}
