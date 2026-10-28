export const CHAT_STUDIO_DEFAULT_MODEL = 'gpt-5.4-mini';
export const CHAT_STUDIO_STORAGE_KEY = 'sub2api:chat-studio:v1';
export class ChatStudioError extends Error {
    constructor(message, options = {}) {
        super(message);
        Object.defineProperty(this, "status", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: void 0
        });
        Object.defineProperty(this, "code", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: void 0
        });
        Object.defineProperty(this, "raw", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: void 0
        });
        this.name = 'ChatStudioError';
        this.status = options.status;
        this.code = options.code;
        this.raw = options.raw;
    }
}
function normalizeContent(content) {
    if (typeof content === 'string')
        return content;
    if (!Array.isArray(content))
        return '';
    return content
        .map((part) => {
        if (typeof part === 'string')
            return part;
        if (!part || typeof part !== 'object')
            return '';
        const record = part;
        if (typeof record.text === 'string')
            return record.text;
        if (typeof record.content === 'string')
            return record.content;
        return '';
    })
        .join('');
}
export function extractChatStudioDelta(payload) {
    if (!payload || typeof payload !== 'object')
        return '';
    const record = payload;
    const choices = Array.isArray(record.choices) ? record.choices : [];
    const firstChoice = choices[0];
    if (firstChoice && typeof firstChoice === 'object') {
        const choice = firstChoice;
        const delta = choice.delta;
        if (delta && typeof delta === 'object') {
            const text = normalizeContent(delta.content);
            if (text)
                return text;
        }
        const message = choice.message;
        if (message && typeof message === 'object') {
            const text = normalizeContent(message.content);
            if (text)
                return text;
        }
        const text = normalizeContent(choice.text);
        if (text)
            return text;
    }
    const outputText = normalizeContent(record.output_text);
    if (outputText)
        return outputText;
    if (Array.isArray(record.output)) {
        return record.output
            .map((item) => {
            if (!item || typeof item !== 'object')
                return '';
            const content = item.content;
            return normalizeContent(content);
        })
            .join('');
    }
    return '';
}
export function isAbortError(error) {
    return !!error && typeof error === 'object' && error.name === 'AbortError';
}
function parseGatewayErrorPayload(text) {
    if (!text.trim())
        return { message: '' };
    try {
        const parsed = JSON.parse(text);
        const error = parsed.error;
        if (error && typeof error === 'object') {
            const errorRecord = error;
            return {
                message: String(errorRecord.message || errorRecord.type || text),
                code: typeof errorRecord.code === 'string' ? errorRecord.code : undefined,
                raw: parsed,
            };
        }
        return {
            message: String(parsed.message || parsed.detail || text),
            code: typeof parsed.code === 'string' ? parsed.code : undefined,
            raw: parsed,
        };
    }
    catch {
        return { message: text };
    }
}
function parseGatewayErrorObject(payload) {
    const error = payload.error;
    if (!error)
        return null;
    if (typeof error === 'string') {
        return { message: error, raw: payload };
    }
    if (typeof error === 'object') {
        const errorRecord = error;
        return {
            message: String(errorRecord.message || errorRecord.type || 'Streaming request failed'),
            code: typeof errorRecord.code === 'string' ? errorRecord.code : undefined,
            raw: payload,
        };
    }
    return { message: 'Streaming request failed', raw: payload };
}
async function throwGatewayError(response) {
    const text = await response.text().catch(() => '');
    const parsed = parseGatewayErrorPayload(text);
    throw new ChatStudioError(parsed.message || response.statusText || `HTTP ${response.status}`, {
        status: response.status,
        code: parsed.code,
        raw: parsed.raw ?? text,
    });
}
export async function listChatModels(apiKey, options = {}) {
    const trimmedKey = apiKey.trim();
    if (!trimmedKey)
        return [];
    const response = await fetch('/v1/models', {
        method: 'GET',
        headers: {
            Authorization: `Bearer ${trimmedKey}`,
        },
        signal: options.signal,
    });
    if (!response.ok) {
        await throwGatewayError(response);
    }
    const payload = await response.json().catch(() => ({}));
    const record = payload && typeof payload === 'object'
        ? payload
        : {};
    const data = Array.isArray(record.data) ? record.data : [];
    return data
        .map((item) => {
        if (typeof item === 'string')
            return { id: item };
        if (!item || typeof item !== 'object')
            return null;
        const model = item;
        const id = typeof model.id === 'string' ? model.id.trim() : '';
        if (!id)
            return null;
        return {
            id,
            display_name: typeof model.display_name === 'string' ? model.display_name : undefined,
            object: typeof model.object === 'string' ? model.object : undefined,
            type: typeof model.type === 'string' ? model.type : undefined,
            owned_by: typeof model.owned_by === 'string' ? model.owned_by : undefined,
            created: typeof model.created === 'number' ? model.created : undefined,
            created_at: typeof model.created_at === 'string' ? model.created_at : undefined,
        };
    })
        .filter((model) => !!model);
}
function readSseEvent(eventText) {
    return eventText
        .split('\n')
        .filter((line) => line.startsWith('data:'))
        .map((line) => line.slice(5).trimStart());
}
function processSseEvent(eventText, onDelta) {
    const dataLines = readSseEvent(eventText);
    if (dataLines.length === 0)
        return { done: false, delta: '' };
    const data = dataLines.join('\n').trim();
    if (!data)
        return { done: false, delta: '' };
    if (data === '[DONE]')
        return { done: true, delta: '' };
    const payload = JSON.parse(data);
    if (payload && typeof payload === 'object') {
        const parsedError = parseGatewayErrorObject(payload);
        if (parsedError) {
            throw new ChatStudioError(parsedError.message, {
                code: parsedError.code,
                raw: parsedError.raw,
            });
        }
    }
    const delta = extractChatStudioDelta(payload);
    if (delta)
        onDelta?.(delta);
    return { done: false, delta };
}
export async function createChatCompletionStream(input) {
    const apiKey = input.apiKey.trim();
    const model = input.model.trim() || CHAT_STUDIO_DEFAULT_MODEL;
    const messages = input.messages
        .map((message) => ({ role: message.role, content: message.content.trim() }))
        .filter((message) => message.content.length > 0);
    if (!apiKey)
        throw new ChatStudioError('Missing API key');
    if (!model)
        throw new ChatStudioError('Missing model');
    if (messages.length === 0)
        throw new ChatStudioError('Missing messages');
    const response = await fetch('/v1/chat/completions', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            Authorization: `Bearer ${apiKey}`,
        },
        body: JSON.stringify({
            model,
            messages,
            stream: true,
        }),
        signal: input.signal,
    });
    if (!response.ok) {
        await throwGatewayError(response);
    }
    if (!response.body) {
        throw new ChatStudioError('Streaming response is not readable', { status: response.status });
    }
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    let content = '';
    let done = false;
    try {
        while (!done) {
            const { value, done: readerDone } = await reader.read();
            if (readerDone)
                break;
            buffer += decoder.decode(value, { stream: true });
            buffer = buffer.replace(/\r\n/g, '\n');
            let separatorIndex = buffer.indexOf('\n\n');
            while (separatorIndex >= 0) {
                const eventText = buffer.slice(0, separatorIndex);
                buffer = buffer.slice(separatorIndex + 2);
                const event = processSseEvent(eventText, input.onDelta);
                done = event.done;
                content += event.delta;
                if (done)
                    break;
                separatorIndex = buffer.indexOf('\n\n');
            }
        }
        const finalText = decoder.decode();
        if (finalText)
            buffer += finalText.replace(/\r\n/g, '\n');
        if (!done && buffer.trim()) {
            const event = processSseEvent(buffer, input.onDelta);
            content += event.delta;
        }
    }
    finally {
        reader.releaseLock();
    }
    return { content };
}
