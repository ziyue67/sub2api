const originalDefaults = {
    harassment: 98, 'harassment/threatening': 90, hate: 65, 'hate/threatening': 65,
    illicit: 95, 'illicit/violent': 95, 'self-harm': 65, 'self-harm/intent': 85,
    'self-harm/instructions': 65, sexual: 65, 'sexual/minors': 65, violence: 95, 'violence/graphic': 95,
};
export const optionalModerationCategories = ['jailbreak', 'control_bypass'];
export function moderationThresholdDefaults(engine) {
    return engine === 'typesafe' ? { ...originalDefaults, jailbreak: 95, control_bypass: 95 } : { ...originalDefaults };
}
export function moderationCategorySettings(engine, saved) {
    return engine === 'typesafe'
        ? Object.fromEntries(optionalModerationCategories.map(category => [category, { enabled: saved?.[category]?.enabled === true }]))
        : {};
}
export function moderationScoreRows(scores, thresholds, engine, settings) {
    const supported = moderationThresholdDefaults(engine);
    return Object.entries(scores)
        .filter(([category]) => category in supported)
        .map(([category, score]) => {
        const threshold = thresholds[category];
        // Older rows have no optional categories. New rows use the recorded setting.
        const optional = optionalModerationCategories.some(id => id === category);
        const enabled = !optional || settings?.[category]?.enabled === true;
        const hit = enabled && Number.isFinite(score) && Number.isFinite(threshold) && score >= threshold;
        return { category, score, threshold, enabled, hit };
    })
        .sort((a, b) => b.score - a.score);
}
