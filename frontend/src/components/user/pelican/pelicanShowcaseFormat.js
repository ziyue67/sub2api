const EFFORTS = new Set(['minimal', 'low', 'medium', 'high', 'xhigh']);
export function pelicanEffortLabel(t, effort) {
    const key = (effort || '').trim().toLowerCase();
    if (!EFFORTS.has(key))
        return '';
    return t('pelicanShowcase.reasoning', { effort: t(`pelicanShowcase.efforts.${key}`, {}) });
}
export function pelicanDurationLabel(t, latencyMs) {
    if (typeof latencyMs !== 'number' || !Number.isFinite(latencyMs) || latencyMs <= 0)
        return '—';
    return t('pelicanShowcase.duration', { seconds: (latencyMs / 1000).toFixed(1) });
}
