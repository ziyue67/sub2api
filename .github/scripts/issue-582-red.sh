#!/usr/bin/env bash
set -euo pipefail
baseline=08d69a522
log="$RUNNER_TEMP/issue-582-baseline-red.log"
targets=(
  backend/internal/repository/scheduler_cache.go
  backend/internal/repository/user_msg_queue_cache.go
  backend/internal/repository/concurrency_cache.go
  backend/internal/repository/api_key_cache.go
  backend/internal/repository/passkey_session_store.go
  backend/internal/pkg/redissession/store.go
)
# Retain the exact-head tests and Redis image override; replace only production
# implementations. Always restore before exact-head GREEN, including on failure.
trap 'git restore --source=HEAD -- "${targets[@]}"' EXIT
for target in "${targets[@]}"; do
  git show "$baseline:$target" > "$target"
done
set +e
(cd backend && go test -tags=integration ./internal/repository -run '^TestIssue582(SnapshotMemberUpdate|SingleUseSessions|FixedWindow|PasskeyTake)$|^TestUserMsgQueueCacheSuite$' -count=1 -v) > "$log" 2>&1
result=$?
set -e
cat "$log"
test "$result" -ne 0
! grep -Eq '\[build failed\]|^FAIL.*\[setup failed\]|--- SKIP:' "$log"
grep -q 'redis_version:6.0.' "$log"
for name in SnapshotMemberUpdate SingleUseSessions FixedWindow PasskeyTake; do
  grep -q -- "--- FAIL: TestIssue582$name" "$log"
done
grep -q -- '--- FAIL: TestUserMsgQueueCacheSuite/TestReconcileExpiredLockCandidatesRefreshesLiveLockIndex' "$log"
grep -q 'ERR syntax error' "$log"
grep -q 'unknown command.*getdel' "$log"
