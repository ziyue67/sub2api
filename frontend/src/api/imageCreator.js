import { apiClient } from './client';
function appendIfPresent(form, key, value) {
    if (value === undefined)
        return;
    const text = String(value).trim();
    if (text === '')
        return;
    form.append(key, text);
}
function buildJSONPayload(input) {
    return {
        api_key_id: input.apiKeyId,
        model: input.model,
        prompt: input.prompt,
        size: input.size,
        quality: input.quality,
        count: input.count,
        output_format: input.outputFormat,
        background: input.background,
    };
}
function buildMultipartPayload(input) {
    const form = new FormData();
    appendIfPresent(form, 'api_key_id', input.apiKeyId);
    appendIfPresent(form, 'model', input.model);
    appendIfPresent(form, 'prompt', input.prompt);
    appendIfPresent(form, 'size', input.size);
    appendIfPresent(form, 'quality', input.quality);
    appendIfPresent(form, 'count', input.count);
    appendIfPresent(form, 'output_format', input.outputFormat);
    appendIfPresent(form, 'background', input.background);
    if (input.referenceImage) {
        form.append('reference_image', input.referenceImage, input.referenceImage.name || 'reference.png');
    }
    return form;
}
export async function createImageTask(input) {
    const payload = input.referenceImage ? buildMultipartPayload(input) : buildJSONPayload(input);
    const { data } = await apiClient.post('/user/image-creator/tasks', payload);
    return data;
}
export async function uploadImageCreatorReference(input) {
    const form = new FormData();
    form.append('api_key_id', String(input.apiKeyId));
    form.append('file', input.file, input.file.name || 'reference.png');
    const { data } = await apiClient.post('/user/image-creator/references', form);
    return data;
}
export async function listImageTasks() {
    const { data } = await apiClient.get('/user/image-creator/tasks');
    return data;
}
export async function listManagedImages(params = {}) {
    const { data } = await apiClient.get('/user/image-creator/images', {
        params,
    });
    return data;
}
export async function deleteManagedImages(ids) {
    const { data } = await apiClient.delete('/user/image-creator/images', {
        data: { ids },
    });
    return data;
}
export async function getImageTask(id) {
    const { data } = await apiClient.get(`/user/image-creator/tasks/${id}`);
    return data;
}
export async function downloadImageFile(url) {
    const token = localStorage.getItem('auth_token');
    const response = await fetch(url, {
        credentials: 'include',
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    });
    if (!response.ok) {
        throw new Error(`Image download failed (${response.status})`);
    }
    return response.blob();
}
