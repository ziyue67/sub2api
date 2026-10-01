#!/usr/bin/env bash
# Run on the existing k3s host; the release tag pins this helper and its image.
set -Eeuo pipefail
version=''
args=()
while (($#)); do
  case "$1" in
    -v|--version) [[ $# -ge 2 ]] || { echo 'Missing version' >&2; exit 2; }; version=$2; shift 2 ;;
    --version=*) version=${1#*=}; shift ;;
    -h|--help) echo 'Usage: bash upgrade-k3s.sh --version vX.Y.Z [--namespace NAME --deployment NAME --dry-run]'; exit 0 ;;
    *) args+=("$1"); shift ;;
  esac
done
[[ $version =~ ^v[0-9]+[.][0-9]+[.][0-9]+$ ]] || { echo 'Specify a stable release tag with --version vX.Y.Z' >&2; exit 2; }
command -v python3 >/dev/null
command -v curl >/dev/null
umask 077
tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
curl -fsSL --retry 2 --connect-timeout 15 --max-time 60 "https://raw.githubusercontent.com/ranxi2001/sub2api/$version/deploy/upgrade-k3s.py" -o "$tmp/upgrade.py"
python3 "$tmp/upgrade.py" --version "$version" "${args[@]}"
