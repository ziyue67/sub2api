function trimEffort(value) {
    if (typeof value !== 'string')
        return '';
    return value.trim();
}
export function parseCodexCatalogModels(content) {
    if (!content)
        return [];
    try {
        const payload = JSON.parse(content);
        if (typeof payload !== 'object' || payload === null || !('models' in payload))
            return [];
        const models = payload.models;
        if (!Array.isArray(models))
            return [];
        return models.flatMap((model) => {
            if (typeof model !== 'object' || model === null || !('slug' in model))
                return [];
            const slug = trimEffort(model.slug);
            if (!slug)
                return [];
            return [{ ...model, slug }];
        });
    }
    catch {
        return [];
    }
}
export function findCodexCatalogModel(content, slug) {
    const wanted = slug.trim();
    if (!wanted)
        return undefined;
    return parseCodexCatalogModels(content).find((model) => model.slug === wanted);
}
export function selectCodexConfigReasoningEffort(model) {
    if (!model)
        return null;
    const efforts = (model.supported_reasoning_levels ?? []).flatMap((level) => {
        const effort = trimEffort(level?.effort);
        return effort ? [effort] : [];
    });
    if (efforts.length === 0)
        return null;
    const defaultLevel = trimEffort(model.default_reasoning_level);
    if (defaultLevel && efforts.includes(defaultLevel)) {
        return defaultLevel === 'none' ? null : defaultLevel;
    }
    return efforts.find((effort) => effort !== 'none') ?? null;
}
export function formatCodexReasoningEffortTomlLine(effort) {
    if (!effort)
        return '';
    return `model_reasoning_effort = "${effort}"\n`;
}
