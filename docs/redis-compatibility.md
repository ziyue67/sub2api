# Redis compatibility

The minimum supported Redis server is **6.0**. Redis 7.x remains suitable for new
installations; existing Redis 6.0 deployments do not need a server upgrade for
scheduler snapshots, shared OAuth/SIWC sessions, passkeys, queues or IP controls.
This is a command compatibility floor, not an upstream Redis maintenance policy.

The #582 audit covered production Go Redis calls and embedded Lua commands across
`backend`, including optional features. The version-sensitive operations are:

| Path | Previously required | Redis 6.0 implementation and preserved behavior |
| --- | --- | --- |
| Scheduler account metadata | `SET … GET` (6.2) | Atomic `GETSET` returns the previous metadata and clears its TTL. Previous-platform detection still advances the global revision; per-platform revisions, chunk error invalidation, hot last-used values, epoch/version fences and old snapshot grace TTLs retain their protections. |
| User message queue and concurrency index cleanup | `ZRANGE … BYSCORE LIMIT` (6.2) | `ZRANGEBYSCORE … LIMIT` keeps bounded score ordering; Lua still rechecks live lock TTLs/slot loads before cleanup. |
| Shared OAuth/SIWC and passkey sessions | `GETDEL` (6.2) | One Lua `GET`/`DEL` operation consumes at most once across replicas. Missing/expired keys miss; wrong types fail without deletion; invalid JSON remains consumed. No independent local session fallback. |
| IP risk lists/recovery and serverless pods | `ZRANGE … REV` / `BYSCORE` (6.2) | `ZREVRANGE`, `ZRANGEBYSCORE`, `ZREVRANGEBYSCORE` retain direction, score bounds, pagination and limits. |
| API key creation fixed window | `EXPIRE … NX` (7.0) | Atomic Lua `INCR`, `TTL`, conditional `EXPIRE` repairs a missing TTL without extending an existing window. |

Other audited commands/options (including `SET NX/EX/PX`, `ZADD XX`, `UNLINK`,
`SCAN`, `TIME`, Lua effects replication and `ACL GETUSER` for regional cutover)
are available by Redis 6.0. The regional identity revocation check uses Redis 6.0
ACLs. This change does not claim support for older servers or Redis Cluster.

The dedicated [Issue 582 workflow](../.github/workflows/issue-582-regression.yml)
runs actual Redis **6.0.20** and **7.4.12** containers in isolated CI. It replays
new behavior tests against production `08d69a522` for a required Redis 6.0 RED,
restores the exact PR head, then checks snapshot writes/readback/member updates,
outbox consumption with database fallback disabled, concurrent publication,
metadata invalidation, failure paths, TTLs, single-use sessions and fixed windows.
Existing queue/concurrency tests run on both real server versions; miniredis unit
tests supplement these checks and do not establish server compatibility.

Official command histories: [SET](https://redis.io/docs/latest/commands/set/),
[GETSET](https://redis.io/docs/latest/commands/getset/),
[ZRANGE](https://redis.io/docs/latest/commands/zrange/),
[GETDEL](https://redis.io/docs/latest/commands/getdel/),
[EXPIRE](https://redis.io/docs/latest/commands/expire/).
