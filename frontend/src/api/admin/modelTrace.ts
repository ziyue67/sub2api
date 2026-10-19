import { buildApiUrl } from '@/api/client'
import { ADMIN_UI_REQUEST_HEADER } from '@/api/adminUIRequest'

export interface ModelTraceChallenge {
  id: string
  prompt: string
  expected_count: number
}

export interface ModelTraceBank {
  source_commit: string
  sha256: string
  built_at: string
  algorithm: string
  models: string[]
}

export interface ModelTraceCandidate {
  model: string
  family: string
  probability: number
}

export interface ModelTraceAnalysis {
  prediction: string
  probability: number
  used_outputs: number
  candidates: ModelTraceCandidate[]
  families: ModelTraceCandidate[]
}

export interface ModelTraceSample {
  challenge: ModelTraceChallenge
  text: string
  mapped_model: string
  environment: { protocol: string; system_prompt: string; oauth: boolean }
  parsed_numbers: number
  minimum_numbers: number
  accepted: boolean
  error?: string
  analysis?: ModelTraceAnalysis
  started_at: string
  latency_ms: number
}

export interface ModelTraceResult {
  requested_model: string
  reasoning_effort: string
  bank: ModelTraceBank
  samples: ModelTraceSample[]
  analysis?: ModelTraceAnalysis
  status: 'complete' | 'partial' | 'insufficient'
  disagreement: boolean
  created_at: string
}

type ModelTraceEvent =
  | { type: 'modeltrace_start'; data: { challenges: ModelTraceChallenge[]; bank: ModelTraceBank } }
  | { type: 'modeltrace_sample'; data: ModelTraceSample }
  | { type: 'modeltrace_complete'; data: ModelTraceResult }

// A completed SSE event is required. A disconnected stream must never become a
// successful round or silently save partial samples as a completed attribution.
export async function runModelTrace(
  accountId: number,
  modelId: string,
  reasoningEffort: string,
  signal: AbortSignal,
  onEvent: (event: ModelTraceEvent) => void
): Promise<ModelTraceResult> {
  const response = await fetch(buildApiUrl(`/admin/accounts/${accountId}/modeltrace-test`), {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${localStorage.getItem('auth_token') || ''}`,
      'Content-Type': 'application/json',
      [ADMIN_UI_REQUEST_HEADER]: '1'
    },
    body: JSON.stringify({ model_id: modelId, reasoning_effort: reasoningEffort }),
    signal
  })
  if (!response.ok) {
    // Use HTTP status only: upstream/provider error bodies must not be copied
    // into browser history or interpreted as attribution results.
    throw new Error(`HTTP ${response.status}`)
  }
  const reader = response.body?.getReader()
  if (!reader) throw new Error('MODELTRACE_NO_STREAM')
  const decoder = new TextDecoder()
  let buffer = ''
  let result: ModelTraceResult | undefined
  function consume(line: string) {
    if (!line.startsWith('data:')) return
    const event = JSON.parse(line.slice(5).trim()) as ModelTraceEvent | { type: 'error'; error?: string }
    if (event.type === 'error') throw new Error('MODELTRACE_STREAM_ERROR')
    if (event.type === 'modeltrace_start' || event.type === 'modeltrace_sample' || event.type === 'modeltrace_complete') {
      onEvent(event)
      if (event.type === 'modeltrace_complete') result = event.data
    }
  }
  try {
    while (!result) {
      const { value, done } = await reader.read()
      buffer += decoder.decode(value, { stream: !done })
      if (buffer.length > 4 * 1024 * 1024) throw new Error('MODELTRACE_STREAM_LIMIT')
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      lines.forEach(consume)
      if (done) { if (buffer.trim()) consume(buffer); break }
    }
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
    if (!result) throw new Error('MODELTRACE_INCOMPLETE_STREAM')
    return result
  } finally {
    await reader.cancel().catch(() => undefined)
    reader.releaseLock()
  }
}
