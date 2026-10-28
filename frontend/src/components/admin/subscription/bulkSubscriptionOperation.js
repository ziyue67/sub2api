const pendingKeys = new Map();
function currentAdminId() {
    try {
        const user = JSON.parse(globalThis.localStorage?.getItem('auth_user') ?? 'null');
        const id = user?.id;
        return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
    }
    catch {
        return null;
    }
}
function readStoredKey(storageKey) {
    try {
        return globalThis.sessionStorage?.getItem(storageKey) ?? null;
    }
    catch {
        return null;
    }
}
function storeKey(storageKey, key) {
    try {
        if (key)
            globalThis.sessionStorage?.setItem(storageKey, key);
        else
            globalThis.sessionStorage?.removeItem(storageKey);
    }
    catch {
        // Keep same-session retries safe in memory when browser storage is unavailable.
    }
}
export function prepareBulkSubscriptionOperation(input) {
    // Send the same canonical payload used for storage, including ID order, so a
    // retry from a differently sorted table still matches the backend fingerprint.
    const request = {
        subscription_ids: [...new Set(input.subscription_ids)].sort((a, b) => a - b),
        action: input.action
    };
    if (input.action === 'extend')
        request.days = input.days;
    if (input.action === 'reset_quota') {
        request.daily = !!input.daily;
        request.weekly = !!input.weekly;
        request.monthly = !!input.monthly;
    }
    const adminId = currentAdminId();
    const storageKey = adminId ? `sub2api:admin:subscription-bulk:${adminId}:${JSON.stringify(request)}` : null;
    let key = storageKey ? pendingKeys.get(storageKey) ?? readStoredKey(storageKey) : null;
    const outcomeUncertain = !!key;
    if (!key) {
        const requestId = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`;
        key = `subscription-bulk-${adminId ?? 'unknown'}-${requestId}`;
    }
    if (storageKey) {
        pendingKeys.set(storageKey, key);
        storeKey(storageKey, key);
    }
    return { request, key, storageKey, outcomeUncertain };
}
export function completeBulkSubscriptionOperation(operation) {
    if (!operation.storageKey)
        return;
    pendingKeys.delete(operation.storageKey);
    storeKey(operation.storageKey, null);
}
