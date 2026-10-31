#!/usr/bin/env bash
# Compare executed production files with immutable Git blobs in RED and GREEN CI.
set -euo pipefail
checkout=$1
expected=$2
shift 2
actual=$(git -C "$checkout" rev-parse HEAD)
test "$actual" = "$expected"
printf 'checkout=%s commit=%s\n' "$checkout" "$actual"
for file in "$@"; do
  committed=$(git -C "$checkout" rev-parse "$expected:$file")
  executed=$(git -C "$checkout" hash-object -- "$file")
  printf '%s committed=%s executed=%s\n' "$file" "$committed" "$executed"
  test "$committed" = "$executed"
done
