export function getCustomMenuLabel(item, locale) {
    const preferredLabel = locale.toLowerCase().startsWith('zh') ? item.label_zh : item.label_en;
    return preferredLabel?.trim() || item.label?.trim() || item.label_en?.trim() || item.label_zh?.trim() || '';
}
