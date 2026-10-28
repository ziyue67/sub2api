import { apiClient } from './client';
function isRecord(value) {
    return typeof value === 'object' && value !== null;
}
function numberFromRecord(value, key, fallback) {
    const raw = value?.[key];
    return typeof raw === 'number' && Number.isFinite(raw) ? raw : fallback;
}
function stringFromRecord(value, key, fallback) {
    const raw = value?.[key];
    return typeof raw === 'string' && raw.trim() ? raw.trim() : fallback;
}
function recordFromUnknown(value) {
    return isRecord(value) ? value : {};
}
function nodeStatusFromRecord(value) {
    const raw = value?.status;
    return raw === 'queued' || raw === 'running' || raw === 'done' || raw === 'failed' ? raw : 'idle';
}
function normalizeRunStatus(status) {
    return status === 'pending' ? 'queued' : status;
}
function toBackendNode(node) {
    return {
        id: node.id,
        type: node.type,
        position: {
            x: node.x,
            y: node.y,
            width: node.width,
            height: node.height,
        },
        data: {
            title: node.title,
            status: node.status,
            config: node.config ?? {},
            result: node.result,
            error: node.error,
        },
    };
}
function fromBackendNode(node) {
    const position = recordFromUnknown(node.position);
    const data = recordFromUnknown(node.data);
    return {
        id: node.id,
        type: node.type,
        title: stringFromRecord(data, 'title', node.id),
        x: numberFromRecord(position, 'x', 80),
        y: numberFromRecord(position, 'y', 80),
        width: numberFromRecord(position, 'width', 170),
        height: numberFromRecord(position, 'height', 86),
        status: nodeStatusFromRecord(data),
        config: recordFromUnknown(data.config),
        result: data.result,
        error: data.error,
    };
}
function toBackendEdge(edge) {
    return {
        id: edge.id,
        source: edge.source_node_id,
        target: edge.target_node_id,
        source_handle: edge.source_handle,
        target_handle: edge.target_handle,
    };
}
function fromBackendEdge(edge) {
    return {
        id: edge.id,
        source_node_id: edge.source,
        target_node_id: edge.target,
        source_handle: edge.source_handle,
        target_handle: edge.target_handle,
    };
}
function toBackendPayload(payload) {
    return {
        title: payload.name,
        description: payload.description || '',
        nodes: payload.document.nodes.map(toBackendNode),
        edges: payload.document.edges.map(toBackendEdge),
        viewport: payload.document.viewport ?? {},
        metadata: {
            ...(payload.document.metadata ?? {}),
            ...(payload.settings ?? {}),
            model: payload.model || undefined,
        },
    };
}
function fromBackendSummary(item) {
    return {
        id: String(item.id),
        name: item.title,
        description: item.description,
        node_count: item.node_count,
        created_at: item.created_at,
        updated_at: item.updated_at,
    };
}
function fromBackendCanvas(item) {
    const metadata = recordFromUnknown(item.metadata);
    return {
        ...fromBackendSummary({
            id: item.id,
            title: item.title,
            description: item.description,
            node_count: item.nodes?.length ?? 0,
            created_at: item.created_at,
            updated_at: item.updated_at,
        }),
        model: typeof metadata.model === 'string' ? metadata.model : undefined,
        document: {
            nodes: (item.nodes ?? []).map(fromBackendNode),
            edges: (item.edges ?? []).map(fromBackendEdge),
            viewport: item.viewport,
            metadata,
        },
        settings: metadata,
    };
}
function fromBackendRun(run) {
    return {
        id: String(run.id),
        canvas_id: run.canvas_id ? String(run.canvas_id) : '',
        status: normalizeRunStatus(run.status),
        api_key_id: run.api_key_id,
        model: run.model,
        started_at: run.started_at,
        completed_at: run.completed_at,
        canceled_at: run.canceled_at,
        error_message: run.error_message,
        result_node_ids: run.result_node_ids,
        input: run.input,
        output: run.output,
        outputs: recordFromUnknown(run.outputs),
        created_at: run.created_at,
        updated_at: run.updated_at,
    };
}
export async function listCanvases(params = {}) {
    const { data } = await apiClient.get('/user/canvases', { params });
    return {
        ...data,
        items: (data.items ?? []).map(fromBackendSummary),
    };
}
export async function getCanvas(id) {
    const { data } = await apiClient.get(`/user/canvases/${id}`);
    return fromBackendCanvas(data.item);
}
export async function createCanvas(payload) {
    const { data } = await apiClient.post('/user/canvases', toBackendPayload(payload));
    return fromBackendCanvas(data.item);
}
export async function updateCanvas(id, payload) {
    const { data } = await apiClient.put(`/user/canvases/${id}`, toBackendPayload(payload));
    return fromBackendCanvas(data.item);
}
export async function deleteCanvas(id) {
    const { data } = await apiClient.delete(`/user/canvases/${id}`);
    return data;
}
export async function listCanvasRuns(params = {}) {
    const { data } = await apiClient.get('/user/canvas-runs', { params });
    return {
        ...data,
        items: (data.items ?? []).map(fromBackendRun),
    };
}
export async function createCanvasRun(payload) {
    const { data } = await apiClient.post('/user/canvas-runs', {
        canvas_id: Number(payload.canvas_id),
        api_key_id: payload.api_key_id,
        model: payload.model,
    });
    return fromBackendRun(data.item);
}
export async function getCanvasRun(id) {
    const { data } = await apiClient.get(`/user/canvas-runs/${id}`);
    return fromBackendRun(data.item);
}
export async function cancelCanvasRun(id) {
    const { data } = await apiClient.post(`/user/canvas-runs/${id}/cancel`);
    return fromBackendRun(data.item);
}
export async function listCanvasModels(apiKeyId) {
    const { data } = await apiClient.get('/user/canvas/models', {
        params: apiKeyId ? { api_key_id: apiKeyId } : undefined,
    });
    return {
        items: (data.items ?? []).map((item) => ({
            id: item.id,
            name: item.display_name || item.name || item.id,
            provider: 'openai',
            capabilities: (item.capabilities ?? []).flatMap((capability) => {
                if (capability === 'image')
                    return ['text_to_image', 'image_to_image'];
                if (capability === 'chat')
                    return ['text', 'prompt'];
                return [];
            }),
            supports_image_input: item.capabilities?.includes('image') ?? false,
            supports_image_output: item.capabilities?.includes('image') ?? false,
        })),
    };
}
