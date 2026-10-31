export default {
  "started": "Request started (local time)",
  "finished": "Request finished (local time)",
  "total": "Total",
  "first_content": "First content token (visible text)",
  "connection": "Connection acquisition",
  "response_headers": "Wait for response headers",
  "stream_output": "Stream output",
  "local_processing": "Local processing (event encoding)",
  "details": "Timing details",
  "overlap": "Offsets are relative to test start. Total includes all stages; first content includes connection and header waiting. Stream output overlaps local work. These measurements must not be added together or interpreted as model compute time.",
  "range": "Observed offsets: {start} → {end}",
  "segments": "Sum of {count} encoding segments",
  "missingFinal": "— Final server measurements are unavailable; no completion time or duration is inferred.",
  "delivery": {
    "idle": "Not started",
    "running": "Waiting for final server measurements",
    "finished": "Server measurements received",
    "legacy": "Timing was not supplied by this server",
    "incomplete": "Stream ended before completion",
    "canceled": "Canceled in this browser; server completion was not confirmed",
    "error": "Connection interrupted; final measurements may be unavailable"
  },
  "outcome": {
    "success": "Success",
    "error": "Error",
    "timeout": "Timeout",
    "canceled": "Canceled"
  },
  "boundaries": {
    "connection": "HTTP GetConn → GotConn, including pool/DNS/TCP/TLS waits; reused connections are marked separately.",
    "response_headers": "Request fully written → complete response headers available. This includes upstream waiting, not just network transit.",
    "first_content": "Start of the single upstream call → first parsed nonblank visible text. Comments, heartbeat, metadata and first byte do not qualify; this is an observation, not tokenizer instrumentation.",
    "stream_output": "First visible text → parser return on completion, error or cancellation; includes reads, parsing, scheduling and local writes, not client receipt.",
    "local_processing": "Only explicit JSON encoding of test SSE events, before each write. Duration sums these disjoint segments; displayed offsets bound them. Excludes upstream waits, writes, final timing event and other uninstrumented local work."
  },
  "reasons": {
    "not_observed": "— Boundary was not observed or not reached.",
    "no_content": "— No nonblank visible text was observed.",
    "not_isolated": "— Local processing was not measured separately.",
    "invalid_observation": "— Observation was outside the test interval.",
    "connection_reused": "— Existing connection reused; no new connection setup is claimed.",
    "multiple_requests": "— Multiple upstream requests/retries cannot be represented as one stage.",
    "non_stream": "— Not applicable to a nonstreaming response.",
    "unsupported_path": "— This path does not expose live content parser boundaries.",
    "collector_limit": "— Collector limit reached; partial measurements are omitted."
  }
}
