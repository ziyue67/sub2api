import { apiClient } from '../client';
export async function readOperations(params, signal) { return (await apiClient.get('/admin/upstream-operations/snapshot', { params, signal })).data; }
