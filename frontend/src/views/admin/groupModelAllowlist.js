export const createModelAllowlistState = (config) => ({
    enabled: config?.enabled ?? false,
    savedModels: normalizeModels(config?.models ?? []),
    items: [],
});
export const hydrateModelAllowlistState = (config, candidates) => {
    const state = createModelAllowlistState(config);
    setModelAllowlistCandidates(state, candidates);
    return state;
};
export const setModelAllowlistCandidates = (state, candidates) => {
    const normalizedCandidates = normalizeModels(candidates);
    const currentSelected = new Set(state.items.filter(item => item.selected).map(item => item.id));
    const currentKnown = new Set(state.items.map(item => item.id));
    const savedSelected = new Set(state.savedModels);
    const hasExistingItems = state.items.length > 0;
    const selectionOrder = normalizeModels([
        ...state.items.map(item => item.id),
        ...state.savedModels,
        ...normalizedCandidates,
    ]);
    state.items = selectionOrder.map(id => {
        const selected = hasExistingItems
            ? currentSelected.has(id)
            : state.savedModels.length > 0
                ? savedSelected.has(id)
                : normalizedCandidates.includes(id);
        return {
            id,
            selected: selected && (currentKnown.has(id) || savedSelected.has(id) || state.savedModels.length === 0),
        };
    });
};
export const toggleModelAllowlistItem = (state, modelID) => {
    const item = state.items.find(item => item.id === modelID);
    if (item) {
        item.selected = !item.selected;
    }
};
export const selectAllModelAllowlistItems = (state) => {
    state.items.forEach(item => {
        item.selected = true;
    });
};
export const invertModelAllowlistSelection = (state) => {
    state.items.forEach(item => {
        item.selected = !item.selected;
    });
};
export const moveModelAllowlistItem = (state, fromIndex, toIndex) => {
    if (fromIndex === toIndex ||
        fromIndex < 0 ||
        toIndex < 0 ||
        fromIndex >= state.items.length ||
        toIndex >= state.items.length) {
        return;
    }
    const [item] = state.items.splice(fromIndex, 1);
    state.items.splice(toIndex, 0, item);
};
// addCustomModelAllowlistItem 把手工输入的条目追加到白名单末尾（选中状态）。
// 去重；`*` 可出现在任意位置。返回错误码或 null（成功）。
export const addCustomModelAllowlistItem = (state, raw) => {
    const entry = raw.trim();
    if (!entry) {
        return 'empty';
    }
    if (state.items.some(item => item.id.toLowerCase() === entry.toLowerCase()) ||
        state.savedModels.some(model => model.toLowerCase() === entry.toLowerCase())) {
        return 'duplicate';
    }
    state.items.push({ id: entry, selected: true });
    return null;
};
export const buildModelAllowlistConfig = (state) => ({
    enabled: state.enabled,
    models: state.items.length > 0
        ? state.items.filter(item => item.selected).map(item => item.id)
        : [...state.savedModels],
});
export const selectedModelAllowlistCount = (state) => state.items.filter(item => item.selected).length;
const normalizeModels = (models) => {
    const seen = new Set();
    const out = [];
    for (const raw of models) {
        const model = raw.trim();
        if (!model || seen.has(model)) {
            continue;
        }
        seen.add(model);
        out.push(model);
    }
    return out;
};
