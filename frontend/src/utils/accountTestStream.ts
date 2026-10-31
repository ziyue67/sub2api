// Consume actual test SSE, including split UTF-8 and a final unterminated line.
// Comments and unknown events remain compatible with older servers/clients.
export async function consumeAccountTestStream(
  response: Response,
  signal: AbortSignal,
  consume: (event: { type: string; [key: string]: unknown }) => void
) {
  const reader = response.body?.getReader()
  if (!reader) throw new Error('No response body')
  const decoder = new TextDecoder()
  let buffer = ''
  let ended = false
  const cancel = () => { void reader.cancel?.().catch(() => {}) }
  signal.addEventListener('abort', cancel, { once: true })
  if (signal.aborted) cancel()
  const line = (value: string) => {
    if (!value.startsWith('data:')) return
    try {
      const event = JSON.parse(value.slice(5).trim())
      if (event && typeof event === 'object' && typeof event.type === 'string') consume(event)
    } catch { /* Ignore malformed/unknown events without exposing their payload. */ }
  }
  try {
    while (true) {
      if (signal.aborted) throw new DOMException('Canceled', 'AbortError')
      const { done, value } = await reader.read()
      if (signal.aborted) throw new DOMException('Canceled', 'AbortError')
      buffer += decoder.decode(value, { stream: !done })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      for (const value of lines) line(value)
      if (done) { if (buffer) line(buffer); ended = true; break }
    }
  } finally {
    signal.removeEventListener('abort', cancel)
    if (!ended) await reader.cancel?.().catch(() => {})
    reader.releaseLock?.()
  }
}
