import { apiClient } from '../client';
const base = '/admin/request-captures';
export async function listTasks(page = 1) {
    return (await apiClient.get(base, { params: { page, page_size: 20 } })).data;
}
export async function createTask(input) {
    return (await apiClient.post(base, input)).data;
}
export async function stopTask(id) { await apiClient.post(base + '/' + id + '/stop'); }
export async function deleteTask(id) { await apiClient.delete(base + '/' + id); }
export async function listRecords(task, page, requestID, errorsOnly) {
    return (await apiClient.get(base + '/' + task + '/requests', { params: { page, page_size: 20, request_id: requestID, errors_only: errorsOnly } })).data;
}
export async function getRecord(task, record) {
    return (await apiClient.get(base + '/' + task + '/requests/' + record)).data;
}
export async function getContent(task, record, part, offset) {
    return (await apiClient.get(base + '/' + task + '/requests/' + record + '/content/' + encodeURIComponent(part), { params: { offset } })).data;
}
export const canStreamExport = () => typeof window.showSaveFilePicker === 'function';
export async function exportCapture(task, record) {
    const picker = window.showSaveFilePicker;
    if (!picker)
        throw new Error('STREAM_SAVE_UNSUPPORTED');
    const handle = await picker.call(window, { suggestedName: 'request-capture-' + (record || task) + '.tar.gz' });
    const writable = await handle.createWritable();
    try {
        const path = base + '/' + task + (record ? '/requests/' + record : '') + '/export';
        const { data } = await apiClient.get(path, { adapter: 'fetch', responseType: 'stream', timeout: 0 });
        await data.pipeTo(writable);
    }
    catch (error) {
        try {
            await writable.abort(error);
        }
        catch { /* pipeTo may already have aborted the file */ }
        throw error;
    }
}
