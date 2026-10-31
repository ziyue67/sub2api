import { ref } from 'vue'

export interface AccountTestMetric {
  start_ms?: number
  end_ms?: number
  duration_ms?: number
  segments?: number
  reason?: string
}
export interface AccountTestTiming {
  version: number
  started_at: string
  finished_at?: string
  total_ms?: number
  outcome: string
  mode: string
  connection: AccountTestMetric
  response_headers: AccountTestMetric
  first_content: AccountTestMetric
  stream_output: AccountTestMetric
  local_processing: AccountTestMetric
}
export type AccountTestDelivery = 'idle' | 'running' | 'finished' | 'legacy' | 'incomplete' | 'canceled' | 'error'

export function useAccountTestTiming() {
  const timing = ref<AccountTestTiming | null>(null)
  const delivery = ref<AccountTestDelivery>('idle')
  let terminal = false
  function reset() { timing.value = null; delivery.value = 'idle'; terminal = false }
  function begin() { reset(); delivery.value = 'running' }
  function accept(event: { type: string; timing?: AccountTestTiming }) {
    // Only timing fields consumed by the dedicated panel enter its view model.
    if (event.type === 'timing' && event.timing?.version === 1) timing.value = event.timing
    if (event.type === 'test_complete' || event.type === 'error') terminal = true
  }
  function finish() {
    if (delivery.value !== 'running') return
    delivery.value = timing.value?.finished_at ? 'finished' : terminal && !timing.value ? 'legacy' : 'incomplete'
  }
  function cancel() { if (delivery.value === 'running') delivery.value = timing.value?.finished_at ? 'finished' : 'canceled' }
  function fail() { if (delivery.value === 'running') delivery.value = timing.value?.finished_at ? 'finished' : 'error' }
  return { timing, delivery, reset, begin, accept, finish, cancel, fail }
}
