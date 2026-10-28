export function defaultOAuthModelMappings(platform) {
    return platform === 'openai' ? [{ from: 'gpt-5.4', to: 'gpt-5.5' }] : [];
}
export function oauthModelMappingsError(rules) {
    if (rules.length > 100)
        return 'autoConfig.mapping.tooMany';
    const seen = new Set();
    const encoder = new TextEncoder();
    for (const rule of rules) {
        const from = rule.from.trim(), to = rule.to.trim();
        const invalidName = (value) => Array.from(value).some(char => {
            const code = char.codePointAt(0);
            return char.trim() === '' || code < 32 || (code >= 127 && code <= 159);
        });
        const sourcePrefix = from.endsWith('*') ? from.slice(0, -1) : from;
        if (!from || !to || encoder.encode(from).length > 256 || encoder.encode(to).length > 256 || invalidName(from + to) || sourcePrefix.includes('*') || to.includes('*'))
            return 'autoConfig.mapping.invalid';
        if (seen.has(from))
            return 'autoConfig.mapping.duplicate';
        seen.add(from);
    }
    return null;
}
export function oauthModelMappingsPayload(rules) {
    return rules.map(rule => ({ from: rule.from.trim(), to: rule.to.trim() }));
}
