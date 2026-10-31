#!/usr/bin/env bash
set -euo pipefail
workspace=$1
red_tree=$2
evidence=$3
review2_tree=$4
ip link set lo up
ip -brief address > "$evidence/storage-network.txt"
pg_bin=$(pg_config --bindir)
fixture_dir=$(mktemp -d /tmp/issue578-storage.XXXXXX)
chmod 755 "$fixture_dir"
mkdir "$fixture_dir/pg"
chown postgres:postgres "$fixture_dir/pg"
cleanup() {
  chmod -R a+rX "$evidence"
  redis-cli -p 56379 shutdown nosave >/dev/null 2>&1 || true
  runuser -u postgres -- "$pg_bin/pg_ctl" -D "$fixture_dir/pg" -m immediate stop >/dev/null 2>&1 || true
  rm -rf "$fixture_dir"
}
trap cleanup EXIT
runuser -u postgres -- "$pg_bin/initdb" -D "$fixture_dir/pg" --auth=trust --no-locale > "$evidence/postgres-init.log"
runuser -u postgres -- "$pg_bin/pg_ctl" -D "$fixture_dir/pg" -l "$fixture_dir/pg/server.log" -o '-h 127.0.0.1 -p 55432 -k /tmp' -w start
redis-server --bind 127.0.0.1 --port 56379 --save '' --appendonly no --daemonize yes --logfile "$fixture_dir/redis.log"
"$pg_bin/createdb" -h 127.0.0.1 -p 55432 -U postgres issue578
export ISSUE578_POSTGRES_DSN='host=127.0.0.1 port=55432 user=postgres dbname=issue578 sslmode=disable'
export ISSUE578_REDIS_ADDR='127.0.0.1:56379'
set +e
(cd "$red_tree/backend" && go test -tags=integration ./internal/integration/issue578 -run '^TestIssue578Review(CrossInstanceHeaderCASRedisSelection|CachePublicationCannotReorder)$' -count=1 -v) > "$evidence/storage-red.log" 2>&1
red_status=$?
set -e
cat "$evidence/storage-red.log"
printf '%s\n' "$red_status" > "$evidence/storage-red-exit.txt"
test "$red_status" -ne 0
if grep -Eq '\[build failed\]|\[setup failed\]|undefined:|syntax error:|sql: Scan error|no tests to run|panic:|--- SKIP:' "$evidence/storage-red.log"; then exit 1; fi
if grep -q 'below-threshold control must actually select' "$evidence/storage-red.log"; then exit 1; fi
grep -F -- '--- FAIL: TestIssue578ReviewCrossInstanceHeaderCASRedisSelection' "$evidence/storage-red.log"
grep -F -- '--- FAIL: TestIssue578ReviewCachePublicationCannotReorder' "$evidence/storage-red.log"
set +e
(cd "$review2_tree/backend" && go test -tags=integration ./internal/integration/issue578 -run '^TestIssue578Review2' -count=1 -v) > "$evidence/storage-review2-red.log" 2>&1
review2_status=$?
set -e
cat "$evidence/storage-review2-red.log"
printf '%s\n' "$review2_status" > "$evidence/storage-review2-red-exit.txt"
test "$review2_status" -ne 0
if grep -Eq '\[build failed\]|\[setup failed\]|undefined:|syntax error:|sql: Scan error|no tests to run|panic:|--- SKIP:' "$evidence/storage-review2-red.log"; then exit 1; fi
if grep -q 'below-threshold control must actually select' "$evidence/storage-review2-red.log"; then exit 1; fi
grep -F -- '--- FAIL: TestIssue578Review2GenericRepositoryPublicationCannotRegress' "$evidence/storage-review2-red.log"
grep -F -- '--- FAIL: TestIssue578Review2PartialPendingCannotRestampOlderWindow' "$evidence/storage-review2-red.log"
(cd "$workspace/backend" && go test -race -tags=integration ./internal/integration/issue578 -run '^TestIssue578Review' -count=1 -v) 2>&1 | tee "$evidence/storage-green.log"
if grep -Eq -- '--- SKIP:|no tests to run' "$evidence/storage-green.log"; then exit 1; fi
cp "$fixture_dir/pg/server.log" "$evidence/postgres-server.log"
cp "$fixture_dir/redis.log" "$evidence/redis-server.log"
