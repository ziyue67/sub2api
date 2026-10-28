import { apiClient } from '../client';
export async function catalog(id, params, signal) { return (await apiClient.get(`/admin/channels/${id}/operations`, { params, signal })).data; }
export async function mutate(id, body) { return (await apiClient.post(`/admin/channels/${id}/operations`, body)).data; }
export async function snapshot(params, signal) { return (await apiClient.get('/admin/channels/operations/snapshot', { params, signal })).data; }
