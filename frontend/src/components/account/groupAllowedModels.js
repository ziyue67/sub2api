/** Reads the per-group model limits stored on the account's group bindings. */
export function groupAllowedModelsFromAccount(account) {
    const limits = {};
    for (const binding of account?.account_groups ?? []) {
        if (binding.allowed_models && binding.allowed_models.length > 0) {
            limits[binding.group_id] = [...binding.allowed_models];
        }
    }
    return limits;
}
/**
 * Builds the update payload: only groups the account stays bound to, and only
 * non-empty lists. The backend treats every group left out as unrestricted.
 */
export function buildGroupAllowedModelsPayload(groupIds, limits) {
    const payload = {};
    for (const groupId of groupIds) {
        const models = (limits[groupId] ?? []).map((model) => model.trim()).filter(Boolean);
        if (models.length > 0) {
            payload[groupId] = [...new Set(models)];
        }
    }
    return payload;
}
