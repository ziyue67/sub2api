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

assert_grep() {
  file=$1
  pattern=$2
  grep -Eq "$pattern" "$file" || fail "$file does not match: $pattern"
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

# 发布用的镜像由 GitHub Actions 工作流（dockerhub.yml）用 deploy/Dockerfile 构建，
# 所以这里校验这条真实路径必须带上运行时回退资源（backend/resources -> /app/resources）。
assert_grep .github/workflows/dockerhub.yml '^[[:space:]]+file: deploy/Dockerfile$'
assert_line deploy/Dockerfile 'COPY --from=backend-builder --chown=sub2api:sub2api /app/backend/resources /app/resources'

# 主 GoReleaser 配置不得再声明 dockers/docker_manifests：
# 镜像只由 main 工作流发布，避免同一次发布出现两份不同 digest 的镜像。
if grep -Eq '^[[:space:]]*(dockers|docker_manifests):' .goreleaser.yaml; then
  fail '.goreleaser.yaml must not declare dockers:/docker_manifests: (images are published by .github/workflows/dockerhub.yml)'
fi

# 简化发布（SIMPLE_RELEASE=true）仍走 GoReleaser 出镜像，同样必须带 backend/resources。
assert_line Dockerfile.goreleaser 'COPY --chown=sub2api:sub2api backend/resources /app/resources'
assert_count .goreleaser.simple.yaml '    dockerfile: Dockerfile.goreleaser' 1
assert_count .goreleaser.simple.yaml '      - backend/resources' 1

printf 'docker runtime resources test passed\n'
