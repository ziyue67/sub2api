# Model capacity diagnostics (#416)

When model-capable accounts are temporarily rate limited, the scheduler's candidate pool may consist entirely of unrelated models. For example, two Sol-capable OAuth accounts can be cooling down while the three remaining API-key accounts only serve Astra or images. The old `pool=3, model_not_supported=3` log omits the two cooling-down accounts.

On the existing failure-only database lookup, diagnostics now count persistently enabled accounts, accounts supporting the routing model, and supporting accounts with an active persisted account rate-limit reset. The failure log records these counts and the earliest reset, without account identities or credentials. Ordinary successful selection adds no query.

For Responses requests, if all supporting accounts have an active persisted rate limit, the HTTP status remains 503 and `error.type` remains `api_error`; `error.code` is `model_capacity_exhausted`. The message identifies the client's requested model and the earliest quota reset in UTC. The `Retry-After` header rounds the remaining delay up to whole seconds. If streaming has already committed headers, the response uses the existing `response.failed` terminal event with the same code and does not attempt to change headers.

Reset time is an estimate of the first quota window clearing, not a guarantee of availability: concurrent requests, other eligibility gates and scheduler snapshot refresh can delay recovery. Unknown/query-error diagnoses retain the conservative 503 fallback without a reset hint. An expired reset or a supporting account without an active account cooldown prevents the all-rate-limited verdict. Per-model/runtime/RPM failures keep their existing behavior.

Responses failure classification uses the channel-mapped routing model for the database lookup, and the original requested model only for the client message. This avoids mistakenly classifying a public alias as unsupported.

## Snapshot recovery inspection

Account rate-limit expiry does not itself write a database row or outbox event. Snapshot reconstruction queries omit accounts whose cooldown is still active, so a removed account can rejoin on an account/group event or the periodic full rebuild. The default full rebuild interval is 300 seconds; the inspected production YAML did not override it. With a healthy rebuild worker, a cooldown expiry can therefore wait until the next rebuild (plus rebuild duration). Disabling periodic rebuilds can extend that delay until another event triggers a rebuild. This change does not add timers or fallback scans to the selection hot path; it reports quota reset rather than promising instantaneous pool recovery.

No database migrations, new settings, automatic model switches, or production account changes are required.

## Validation

- `CGO_ENABLED=0 GOMAXPROCS=2 go test -tags=unit ./internal/service ./internal/handler -count=1`: passed (service 232.915s, handler 48.401s).
- Explicit regression run for the five-account scenario, JSON code/header, conservative fallbacks and committed Responses stream: passed.
- `CGO_ENABLED=0 GOMAXPROCS=2 go build -o /tmp/sub2api-issue416-server ./cmd/server`: passed.
- `git diff --check`: passed.

Validation ran on the local development host with offline/mock tests. No production binary or configuration was changed; database-backed integration tests and live generation were not run.
