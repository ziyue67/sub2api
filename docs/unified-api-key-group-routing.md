# Unified API key group routing

## Problem and behavior

Previously `group_ids` selected the cheapest available group after
`group_routes` had selected a route. With both settings present, the second
selector could override priority, a disabled route, or a route cooldown.
The three regression cases use group A at 1x and group B at 0.5x. The base
`75a342455` selected B in all three cases where route policy required A.

New keys use one `group_routing` policy and one editor:

* `lowest_rate`: ready capacity first, then effective multiplier (including
  user overrides, peak hours and the existing image/video multiplier rules),
  then load. This does not promise the lowest final price across different
  models or price tables.
* `priority_weighted`: ready capacity first, then lowest priority number;
  equal-priority candidates share requests by weight within each process.
* Both strategies share enabled flags, cooldowns and an explicit failover
  switch. All-disabled policies fail closed; they never fall back to the
  projected primary group. Exhaustion returns an error rather than looping.
* Retry selection reuses request permissions, model restrictions and
  subscription entitlement checks. Handler and billing context follow the
  selected group. Selection itself never debits a balance.

Example (submit without `group_id`, `group_ids` or `group_routes`):

```json
{
  "group_routing": {
    "strategy": "priority_weighted",
    "failover": true,
    "routes": [
      {"group_id": 1, "priority": 1, "weight": 1, "enabled": true, "cooldown_seconds": 60},
      {"group_id": 2, "priority": 2, "weight": 1, "enabled": true, "cooldown_seconds": 60}
    ]
  }
}
```

## Compatibility and operational boundaries

Migration 337 adds nullable JSONB. Existing rows are not bulk-converted.
Opening and saving a legacy key without explicitly choosing a strategy omits
routing fields. Legacy route-only keys keep primary-first behavior. Mixed
legacy keys honor route restrictions instead of allowing the cheapest-group
selector to override them; this is an intentional correction of the conflict.
Conversion previews the union of legacy candidates and preserves route values.

Canonical policies project into legacy binding fields for list/filter
compatibility, but those fields are not separate routing authorities. Mixed
new writes are rejected; old API clients cannot edit canonical routing via
legacy fields. Name, quota and status updates remain supported. Administrative
group replacement/removal updates policy JSON in the same transaction or SQL
statement as bindings. Auth snapshot version 34 includes the cloned policy.

Canonical cooldowns are shared in Redis by key and group, expire automatically,
and concurrent failures cannot shorten an existing cooldown. A shared-store
error fails selection closed. Legacy cooldown behavior is unchanged.

The failover switch uses existing safe retry points; it does not add replay
after emitted output or accepted async work. In-request canonical retries
stay on the selected handler's platform. Cross-platform initial selection is
supported by existing dispatch, but not an arbitrary mid-request protocol
change. WebSocket sessions keep their chosen group. Endpoints without an
existing retry loop do not acquire one merely by enabling this switch.

Canonical model discovery and billing declarations enumerate enabled,
authorized candidates even when zero or one is enabled. Billing declarations
use the existing v2 multi-group schema, with per-group rates and no misleading
top-level scalar multiplier. Regional gateways explicitly reject canonical
routing until they support this policy.

Rollback to a binary unaware of `group_routing` is safe only before canonical
keys are enabled. Afterwards the old binary would interpret compatibility
projections as legacy configuration; restore reviewed legacy policy first.

## Verification

The original three conflict regressions fail on base `75a342455` and pass with
this change. Added coverage includes initial selection/failover consistency,
weighted selection, disabled/cooling/unavailable/busy groups, exhaustion,
model denial, shared cooldown errors/expiry/isolation, snapshot isolation,
atomic update masks, SQLite rollback and actual billing-service group/rate
selection. Existing queue, billing, group and admission tests are retained.

Frontend tests cover one editor, lossless strategy changes, unique candidates,
legacy conversion only on explicit selection, provider filtering without
discarding candidates, API serialization and existing page behavior.

`Unified key group routing` CI runs these backend paths with race detection
and runs real PostgreSQL migrations plus policy roundtrip, administrative
replacement and cascade deletion. The normal repository CI also runs the
broader unit, integration, frontend and build checks. CI results must be checked
on the exact PR head; local Docker-unavailable skips are not validation.

No production traffic, customer settings or paid model requests are required
by these tests. This change does not alter cache TTL or cache billing policy.
