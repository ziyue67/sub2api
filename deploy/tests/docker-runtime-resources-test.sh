#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

fail() {
  printf 'docker runtime resources test failed: %s\n' "$1" >&2
  exit 1
}

assert_line() {
  file=$1
  line=$2
  grep -Fqx "$line" "$file" || fail "$file is missing: $line"
}

assert_count() {
  file=$1
  line=$2
  expected=$3
  actual=$(grep -Fxc "$line" "$file" || true)
  [ "$actual" -eq "$expected" ] || fail "$file has $actual occurrences of '$line', expected $expected"
}

test -s backend/resources/model-pricing/model_prices_and_context_window.json || \
  fail 'fallback pricing data is missing or empty'

assert_line Dockerfile.goreleaser 'COPY --chown=sub2api:sub2api backend/resources /app/resources'
assert_line deploy/Dockerfile 'COPY --from=backend-builder --chown=sub2api:sub2api /app/backend/resources /app/resources'
# 每个 dockers 条目都必须带 backend/resources（运行时回退资源）。
# 条目数从配置里的 dockers 段落推导，避免增删架构（如去掉 arm64）时这里写死的数字再次过时。
docker_entries=$(awk '/^dockers:/{f=1;next} /^[a-z_]+:/{f=0} f && /^  - id:/{n++} END{print n+0}' .goreleaser.yaml)
[ "$docker_entries" -gt 0 ] || fail 'no dockers entries found in .goreleaser.yaml'
assert_count .goreleaser.yaml '      - backend/resources' "$docker_entries"
assert_count .goreleaser.simple.yaml '      - backend/resources' 1

printf 'docker runtime resources test passed\n'
