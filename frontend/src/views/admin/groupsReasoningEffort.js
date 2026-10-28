const openAIReasoningEffortValues = [
    "minimal",
    "low",
    "medium",
    "high",
    "xhigh",
    "max",
    "ultra",
];
const anthropicReasoningEffortValues = [
    "low",
    "medium",
    "high",
    "xhigh",
    "max",
];
const reasoningEffortMatchTypes = [
    "exact",
    "prefix",
    "suffix",
];
export const reasoningEffortOverLimitDowngrade = "downgrade";
export const reasoningEffortOverLimitDeny = "deny";
export const reasoningEffortMappingDeny = "deny";
const reasoningEffortValuesForPlatform = (platform) => platform === "anthropic"
    ? anthropicReasoningEffortValues
    : supportsReasoningEffortPolicyPlatform(platform)
        ? openAIReasoningEffortValues
        : [];
export function supportsReasoningEffortPolicyPlatform(platform) {
    return platform === "anthropic" || platform === "openai" || platform === "composite";
}
export function reasoningEffortOptionsForPlatform(platform) {
    return reasoningEffortValuesForPlatform(platform).map((value) => ({
        value,
        label: value,
    }));
}
export function reasoningEffortSourceOptionsForPlatform(platform) {
    return (supportsReasoningEffortPolicyPlatform(platform)
        ? ["none", ...reasoningEffortValuesForPlatform(platform)]
        : []).map((value) => ({ value, label: value }));
}
export function reasoningEffortTargetOptionsForPlatform(platform) {
    const options = reasoningEffortOptionsForPlatform(platform);
    if (options.length === 0)
        return options;
    return [
        ...options,
        { value: reasoningEffortMappingDeny, label: reasoningEffortMappingDeny },
    ];
}
export function normalizeReasoningEffortForPlatform(platform, value) {
    const normalized = value?.trim().toLowerCase() ?? "";
    return reasoningEffortValuesForPlatform(platform).some((allowed) => allowed === normalized)
        ? normalized
        : "";
}
export function normalizeReasoningEffortSourceForPlatform(platform, value) {
    const normalized = value?.trim().toLowerCase() ?? "";
    return supportsReasoningEffortPolicyPlatform(platform) &&
        normalized === "none"
        ? "none"
        : normalizeReasoningEffortForPlatform(platform, value);
}
export function normalizeReasoningEffortTargetForPlatform(platform, value) {
    const normalized = value?.trim().toLowerCase() ?? "";
    return supportsReasoningEffortPolicyPlatform(platform) &&
        normalized === reasoningEffortMappingDeny
        ? reasoningEffortMappingDeny
        : normalizeReasoningEffortForPlatform(platform, value);
}
export function normalizeReasoningEffortMatchType(value) {
    const normalized = value?.trim().toLowerCase() ?? "";
    return reasoningEffortMatchTypes.some((allowed) => allowed === normalized)
        ? normalized
        : "";
}
export function normalizeReasoningEffortOverLimit(value) {
    return value?.trim().toLowerCase() === reasoningEffortOverLimitDeny
        ? reasoningEffortOverLimitDeny
        : reasoningEffortOverLimitDowngrade;
}
let nextMappingRowID = 0;
export function createReasoningEffortMappingPair(pair = {}) {
    nextMappingRowID += 1;
    return {
        id: `reasoning-effort-pair-${nextMappingRowID}`,
        from: pair.from ?? "",
        to: pair.to ?? "",
    };
}
export function createReasoningEffortMappingRow(mapping = {}) {
    nextMappingRowID += 1;
    return {
        id: `reasoning-effort-group-${nextMappingRowID}`,
        match_type: normalizeReasoningEffortMatchType(mapping.match_type),
        model: mapping.model?.trim() ?? "",
        pairs: [
            createReasoningEffortMappingPair({
                from: mapping.from,
                to: mapping.to,
            }),
        ],
    };
}
function mappingScopeKey(matchType, model) {
    const trimmedModel = model?.trim().toLowerCase() ?? "";
    const normalizedType = trimmedModel
        ? normalizeReasoningEffortMatchType(matchType) || "exact"
        : "";
    return `${normalizedType}\0${trimmedModel}`;
}
export function reasoningEffortMappingsToRows(mappings, platform = "openai") {
    const groups = [];
    const indexByScope = new Map();
    (mappings ?? []).forEach((mapping) => {
        const from = normalizeReasoningEffortSourceForPlatform(platform, mapping.from);
        const to = normalizeReasoningEffortTargetForPlatform(platform, mapping.to);
        if (!from || !to)
            return;
        const matchType = normalizeReasoningEffortMatchType(mapping.match_type);
        const model = mapping.model?.trim() ?? "";
        const scope = mappingScopeKey(matchType, model);
        const existingIndex = indexByScope.get(scope);
        if (existingIndex !== undefined) {
            groups[existingIndex].pairs.push(createReasoningEffortMappingPair({ from, to }));
            return;
        }
        const group = createReasoningEffortMappingRow({
            from,
            to,
            match_type: matchType,
            model,
        });
        indexByScope.set(scope, groups.length);
        groups.push(group);
    });
    return groups;
}
export function reasoningEffortMappingsToAPI(rows) {
    return rows.flatMap((row) => {
        const model = row.model.trim();
        return row.pairs.map((pair) => {
            const mapping = {
                from: pair.from.trim(),
                to: pair.to.trim(),
            };
            if (model) {
                mapping.match_type =
                    normalizeReasoningEffortMatchType(row.match_type) || "exact";
                mapping.model = model;
            }
            return mapping;
        });
    });
}
export function validateReasoningEffortMappings(rows, platform = "openai") {
    const errors = {};
    const scopeGroups = new Map();
    const sourcePairs = new Map();
    rows.forEach((row) => {
        const rawMatchType = row.match_type.trim().toLowerCase();
        const matchType = normalizeReasoningEffortMatchType(row.match_type);
        if (rawMatchType && !matchType) {
            errors[row.id] = { ...errors[row.id], match_type: "unsupportedMatchType" };
        }
        const scope = mappingScopeKey(row.match_type, row.model);
        scopeGroups.set(scope, [...(scopeGroups.get(scope) ?? []), row]);
        row.pairs.forEach((pair) => {
            const from = pair.from.trim();
            const to = pair.to.trim();
            if (!from) {
                errors[pair.id] = { ...errors[pair.id], from: "fromRequired" };
            }
            else if (!normalizeReasoningEffortSourceForPlatform(platform, from)) {
                errors[pair.id] = { ...errors[pair.id], from: "unsupportedFrom" };
            }
            else {
                const key = `${scope}\0${from.toLowerCase()}`;
                sourcePairs.set(key, [...(sourcePairs.get(key) ?? []), pair]);
            }
            if (!to) {
                errors[pair.id] = { ...errors[pair.id], to: "toRequired" };
            }
            else if (!normalizeReasoningEffortTargetForPlatform(platform, to)) {
                errors[pair.id] = { ...errors[pair.id], to: "unsupportedTo" };
            }
        });
    });
    scopeGroups.forEach((duplicateGroups) => {
        if (duplicateGroups.length < 2)
            return;
        duplicateGroups.forEach((row) => {
            errors[row.id] = { ...errors[row.id], duplicateScope: "duplicateScope" };
        });
    });
    sourcePairs.forEach((duplicatePairs) => {
        if (duplicatePairs.length < 2)
            return;
        duplicatePairs.forEach((pair) => {
            errors[pair.id] = { ...errors[pair.id], from: "duplicateFrom" };
        });
    });
    return errors;
}
