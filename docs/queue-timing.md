# Queue timing contract

## Observation boundaries

A = authenticated ingress HTTP logger timestamp / accepted WS response.create frame timestamp, before turn-slot or pool waits. F = actual first forward boundary for the reported scope. L = legacy timer baseline. E/T = terminal/first-token observation used by legacy result, sampled before callback cleanup.

| Path | Legacy L, first/duration and TPS denominator | New measured queue / server metrics | Retry scope |
| --- | --- | --- | --- |
| HTTP Responses | Preserve existing final attempt L after admission; TPS output / legacy duration | Q = F-A; server first = T-A, total = E-A, source HTTP ingress pre-final-forward | Earlier failed account attempts are pre-final-forward elapsed time, not pure queue; name/source disclose it |
| HTTP Chat wrapper | Preserve inner forward baseline and duration | Capture inner F explicitly; never infer from wrapper return time minus duration; absent ingress => unknown | Preserve inner retry scope; no wrapper cleanup in Q |
| WS ctx_pool | Preserve legacy sendAndRelay L after final admission, TPS output / legacy duration | Capture A at frame receipt before BeforeTurn and lease; F=L; Q=F-A; server = legacy + Q | Internal final-attempt retries retain A; pre-final-forward includes earlier attempts, source discloses it |
| WS http_bridge | Restore legacy helper-entry L before its admission, including rejected-field retries; TPS output / full legacy duration | A before BeforeTurn; freeze F after initial admission, before first HTTP send; server = legacy + (L-A); forward = legacy - (F-L) | Freeze F once: failed HTTP network attempts stay in forward scope, never move to Q |
| WS passthrough | Preserve first-turn post-read/capture L and later-turn post-payload-preflight L (already includes admission), first/duration and TPS denominator | Capture F at upstream write, Q=F-A; server = legacy + (L-A); forward = legacy - (F-L), never subtract/add the overlapping queue twice | Each accepted serialized turn gets independent A/F; no callback-time reconstruction |

Persist explicit queue-timing source and separately derived server/forward observations; never assume all legacy fields have the same scope. Historical/no-ingress/invalid clock remains NULL/unknown; measured zero remains 0. Validate timestamps then clamp Q to [0,1h] consistently; clamping does not relabel network retries or cleanup as queue. Client E2E/upload remain null/not_collected/source=none. Legacy ops SQL, billing/profit provenance and routing remain unchanged; new queue-inclusive cohorts include only rows with explicit measured source, with bigint arithmetic. Existing failures that produce no usage row stay outside the usage denominator and are disclosed.

Migrations 320 and 321 remain immutable. Migration 322 adds nullable JSON observations without backfilling rows. A binary rollback leaves all columns in place; it does not undo migrations.

## Additive API and operations fields

`queue_wait_ms` remains nullable and is capped at 3,600,000 ms. `queue_timing` is nullable JSON with `status=measured`, a sampled `source`, and an explicit retry `scope`. Its `server_first_token_ms` (nullable for nonstream/missing token), `server_duration_ms`, `forward_first_token_ms`, and `forward_duration_ms` use their own boundaries; `queue_clamped=true` means the capped queue value must not be used to reconstruct the complete server total. A missing JSON observation is unknown even when an older row has a queue value. Unsupported Chat conversion branches without an explicit inner boundary remain unmeasured.

HTTP/ctx_pool use `pre_final_forward`: earlier failed attempts can be inside this elapsed interval. Bridge freezes its first HTTP send within the final account, retaining rejected-field retries in forwarding. Passthrough retains the historical preflight baseline for legacy consumers, while its new arrival is the actual socket frame receipt. The historical `legacy_forward` label does not erase these protocol differences; scheduling/operations/TPS continue to consume the same legacy fields.

Upstream/profit/channel summaries and buckets add `queue_measured_requests`, `queue_unknown_requests`, `queue_inclusive_samples`, `queue_inclusive_p90_ms`, `queue_inclusive_duration_samples`, `queue_inclusive_duration_p90_ms`, `queue_timing_sources`, and `queue_timing_scope`. Existing `p50_ms`/`p90_ms`/`p95_ms`/`duration_p90_ms` retain their prior cohort and values. New quantiles require measured source metadata plus the original token/cost evidence and validity filters. They do not coalesce historical NULL to zero, and their numeric observations are cast to bigint. Profit estimates retain the original standard-charge cost base and attribution. Failures without usage rows are absent from both usage denominators.

The queue observation changes no busy-slot rejection, account admission ordering, retry permission, routing, pricing, ledger or procurement policy. Client E2E/upload remain uncollected. A binary rollback leaves 320/321/322 in place; the migration regression exercises an old-column SQL projection, not an actual old binary rollback.
