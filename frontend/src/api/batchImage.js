import { buildGatewayUrl } from './client';
async function parseBatchImageError(response) {
    try {
        const body = await response.json();
        const message = body?.error?.message || body?.message || response.statusText;
        const error = new Error(message);
        error.code = body?.error?.code || response.status;
        error.status = response.status;
        error.requestId = response.headers.get('X-Request-Id') || '';
        return error;
    }
    catch {
        const error = new Error(response.statusText || `HTTP ${response.status}`);
        error.code = response.status;
        error.status = response.status;
        error.requestId = response.headers.get('X-Request-Id') || '';
        return error;
    }
}
function authHeaders(apiKey, extra) {
    return {
        Authorization: `Bearer ${apiKey}`,
        ...extra,
    };
}
export async function submitBatchImageJob(apiKey, payload, idempotencyKey) {
    const response = await fetch(buildGatewayUrl('/v1/images/batches'), {
        method: 'POST',
        headers: authHeaders(apiKey, {
            'Content-Type': 'application/json',
            'Idempotency-Key': idempotencyKey,
        }),
        body: JSON.stringify(payload),
    });
    if (!response.ok)
        throw await parseBatchImageError(response);
    return response.json();
}
export async function getBatchImageJob(apiKey, batchId) {
    const response = await fetch(buildGatewayUrl(`/v1/images/batches/${encodeURIComponent(batchId)}`), {
        headers: authHeaders(apiKey),
    });
    if (!response.ok)
        throw await parseBatchImageError(response);
    return response.json();
}
export async function listBatchImageJobs(apiKey, options = 20) {
    const params = new URLSearchParams();
    if (typeof options === 'number') {
        params.set('limit', String(options));
    }
    else {
        params.set('limit', String(options.limit || 20));
        if (options.cursor)
            params.set('cursor', options.cursor);
        if (options.status)
            params.set('status', options.status);
        if (options.taskName)
            params.set('task_name', options.taskName);
        if (options.downloaded)
            params.set('downloaded', options.downloaded);
        if (options.from)
            params.set('from', options.from);
        if (options.to)
            params.set('to', options.to);
    }
    const response = await fetch(buildGatewayUrl(`/v1/images/batches?${params.toString()}`), {
        headers: authHeaders(apiKey),
    });
    if (!response.ok)
        throw await parseBatchImageError(response);
    return response.json();
}
export async function listBatchImageModels(apiKey) {
    const response = await fetch(buildGatewayUrl('/v1/images/batches/models'), {
        headers: authHeaders(apiKey),
    });
    if (!response.ok)
        throw await parseBatchImageError(response);
    return response.json();
}
export async function listBatchImageItems(apiKey, batchId, status = '') {
    const query = status ? `?status=${encodeURIComponent(status)}` : '';
    const response = await fetch(buildGatewayUrl(`/v1/images/batches/${encodeURIComponent(batchId)}/items${query}`), {
        headers: authHeaders(apiKey),
    });
    if (!response.ok)
        throw await parseBatchImageError(response);
    return response.json();
}
export async function cancelBatchImageJob(apiKey, batchId) {
    const response = await fetch(buildGatewayUrl(`/v1/images/batches/${encodeURIComponent(batchId)}/cancel`), {
        method: 'POST',
        headers: authHeaders(apiKey),
    });
    if (!response.ok)
        throw await parseBatchImageError(response);
    return response.json();
}
export async function downloadBatchImageZip(apiKey, batchId) {
    const response = await fetch(buildGatewayUrl(`/v1/images/batches/${encodeURIComponent(batchId)}/download`), {
        headers: authHeaders(apiKey),
    });
    if (!response.ok)
        throw await parseBatchImageError(response);
    return response.blob();
}
export async function getBatchImageItemContent(apiKey, batchId, customId, imageIndex = 0) {
    const response = await fetch(buildGatewayUrl(`/v1/images/batches/${encodeURIComponent(batchId)}/items/${encodeURIComponent(customId)}/content?image_index=${encodeURIComponent(String(imageIndex))}`), {
        headers: authHeaders(apiKey),
    });
    if (!response.ok)
        throw await parseBatchImageError(response);
    return response.blob();
}
export async function deleteBatchImageJobRecord(apiKey, batchId) {
    const response = await fetch(buildGatewayUrl(`/v1/images/batches/${encodeURIComponent(batchId)}`), {
        method: 'DELETE',
        headers: authHeaders(apiKey),
    });
    if (!response.ok)
        throw await parseBatchImageError(response);
}
export function saveBlob(blob, filename) {
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = filename;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
    URL.revokeObjectURL(url);
}
