export interface OpenAITwoFAAdmissionConfig {
  enabled: boolean
  model_id: string
  reasoning_effort: string
  wait_seconds: number
  pass_count: number
  passed_concurrency: number
  isolation_count: number
  isolation_group_id: number
}

export function defaultOpenAITwoFAAdmissionConfig(): OpenAITwoFAAdmissionConfig {
  return { enabled: false, model_id: 'gpt-6.1-sol', reasoning_effort: 'low', wait_seconds: 60, pass_count: 6, passed_concurrency: 2, isolation_count: 6, isolation_group_id: 0 }
}

export function openaiTwoFAAdmissionError(c: OpenAITwoFAAdmissionConfig): string | null {
  const integers: [number, number][] = [[c.wait_seconds, 86400], [c.pass_count, 1000], [c.passed_concurrency, 10000], [c.isolation_count, 1000]]
  if (!c.model_id || c.model_id.length > 100 || /[\s*]/.test(c.model_id) || !['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra'].includes(c.reasoning_effort) ||
    integers.some(([n, max]) => !Number.isInteger(n) || n < 1 || n > max) || !Number.isInteger(c.isolation_group_id) || c.isolation_group_id < 0 || c.enabled && !c.isolation_group_id) return 'autoConfig.admission.invalid'
  return null
}
