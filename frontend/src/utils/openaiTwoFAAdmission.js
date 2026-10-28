export function defaultOpenAITwoFAAdmissionConfig() {
    return { enabled: false, model_id: 'gpt-6.1-sol', reasoning_effort: 'low', wait_seconds: 60, pass_count: 6, passed_concurrency: 2, isolation_count: 6, isolation_group_id: 0 };
}
export function openaiTwoFAAdmissionError(c) {
    const integers = [[c.wait_seconds, 86400], [c.pass_count, 1000], [c.passed_concurrency, 10000], [c.isolation_count, 1000]];
    if (!c.model_id || c.model_id.length > 100 || /[\s*]/.test(c.model_id) || !['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra'].includes(c.reasoning_effort) ||
        integers.some(([n, max]) => !Number.isInteger(n) || n < 1 || n > max) || !Number.isInteger(c.isolation_group_id) || c.isolation_group_id < 0 || c.enabled && !c.isolation_group_id)
        return 'autoConfig.admission.invalid';
    return null;
}
