#!/usr/bin/env bash
# Run after Playwright installs its system dependencies and refreshes APT metadata.
# Keep the APT-selected package bytes while bypassing slow regional HTTP mirrors.
set -euo pipefail

FONT_METADATA="${RUNNER_TEMP:?RUNNER_TEMP is required}/fonts-noto-cjk.metadata"
FONT_DEB="$RUNNER_TEMP/fonts-noto-cjk.deb"
apt-cache show fonts-noto-cjk > "$FONT_METADATA"
FONT_ARCHIVE_PATH=$(awk '/^Filename:/ {print $2; exit}' "$FONT_METADATA")
FONT_SHA256=$(awk '/^SHA256:/ {print $2; exit}' "$FONT_METADATA")
[[ "$FONT_ARCHIVE_PATH" == pool/main/f/fonts-noto-cjk/fonts-noto-cjk_*_all.deb ]]
[[ "$FONT_SHA256" =~ ^[0-9a-fA-F]{64}$ ]]
curl --fail --location --retry 3 --retry-all-errors --retry-delay 2 \
  --retry-max-time 300 --connect-timeout 15 --max-time 120 \
  "https://archive.ubuntu.com/ubuntu/$FONT_ARCHIVE_PATH" --output "$FONT_DEB"
printf '%s  %s\n' "$FONT_SHA256" "$FONT_DEB" | sha256sum --check --
sudo dpkg -i "$FONT_DEB"
