export function parseExtraImageModels(value) {
    return [...new Set(value.split(/[\n,]/).map(entry => entry.trim()).filter(Boolean))];
}
export function readExtraImageModels(value) {
    return Array.isArray(value) ? value.filter((entry) => typeof entry === 'string').join('\n') : '';
}
