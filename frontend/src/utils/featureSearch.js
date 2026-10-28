const normalize = (value) => value.normalize('NFKC').toLowerCase().replace(/[_/.-]+/g, ' ').trim();
// The caller supplies the already-filtered sidebar. Never index all router records:
// doing so would surface features hidden by permissions, feature flags or simple mode.
export function buildFeatureSearchEntries(items, routeKeywords = () => '') {
    const entries = [];
    const seen = new Set();
    function visit(nodes, parents) {
        for (const item of nodes) {
            if (!item.expandOnly && !seen.has(item.path)) {
                seen.add(item.path);
                entries.push({
                    path: item.path,
                    label: item.label,
                    group: parents.join(' / '),
                    keywords: normalize([item.label, item.keywords, ...parents, item.path, routeKeywords(item.path)].join(' '))
                });
            }
            if (item.children)
                visit(item.children, [...parents, item.label]);
        }
    }
    visit(items, []);
    return entries;
}
export function searchFeatures(entries, query) {
    const term = normalize(query);
    if (!term)
        return entries;
    const tokens = term.split(/\s+/);
    const rank = (entry) => {
        const label = normalize(entry.label);
        return label === term ? 0 : label.startsWith(term) ? 1 : label.includes(term) ? 2 : 3;
    };
    return entries.filter(entry => tokens.every(token => entry.keywords.includes(token)))
        .sort((a, b) => rank(a) - rank(b));
}
