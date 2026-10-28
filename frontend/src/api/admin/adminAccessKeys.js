import { apiClient } from '../client';
const base = '/admin/settings/admin-api-keys';
export async function list() { return (await apiClient.get(base)).data; }
export async function create(input) { return (await apiClient.post(base, input)).data; }
export async function update(id, input) { return (await apiClient.put(`${base}/${encodeURIComponent(id)}`, input)).data; }
export async function rotate(id) { return (await apiClient.post(`${base}/${encodeURIComponent(id)}/rotate`)).data; }
export async function revoke(id) { return (await apiClient.delete(`${base}/${encodeURIComponent(id)}`)).data; }
export async function deleteRecord(id) { return (await apiClient.delete(`${base}/${encodeURIComponent(id)}/record`)).data; }
