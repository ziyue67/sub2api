#!/usr/bin/env bash
set -euo pipefail
: "${VERSION:?set VERSION}"
: "${ARCH:?set ARCH}"
[[ $VERSION =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]]
[[ $ARCH == amd64 || $ARCH == arm64 ]]
runtime_output=$(mktemp -d)
trap 'rm -rf "$runtime_output"' EXIT
docker buildx build --platform "linux/$ARCH" -f tools/reauth-runtime/Dockerfile \
  --output "type=local,dest=$runtime_output" .
mkdir -p reauth-output
tar --dereference -czf "reauth-output/sub2api-reauth_${VERSION}_linux_${ARCH}.tar.gz" -C "$runtime_output" .
sha256sum "reauth-output/sub2api-reauth_${VERSION}_linux_${ARCH}.tar.gz"
