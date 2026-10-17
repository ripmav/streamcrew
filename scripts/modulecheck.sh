#!/usr/bin/env bash
# Builds a file:// proxy for the given archive and builds modulecheck against it.
set -euo pipefail
zip=${1:-/tmp/streamcrew-src.zip}
cache="$(mktemp -d)/gopath"
mkdir -p "$cache/github.com/ripmav/streamcrew/@v"
ver=v0.0.0-00010101000000-000000000000
cp "$zip" "$cache/github.com/ripmav/streamcrew/@v/$ver.zip"
cp "$(git rev-parse --show-toplevel)/go.mod" "$cache/github.com/ripmav/streamcrew/@v/$ver.mod"
printf '{"Version":"%s","Time":"2000-01-01T00:00:00Z"}' "$ver" > "$cache/github.com/ripmav/streamcrew/@v/$ver.info"
echo "$ver" > "$cache/github.com/ripmav/streamcrew/@v/list"
cd "$(git rev-parse --show-toplevel)/scripts/modulecheck"
GOFLAGS=-mod=mod GOPROXY="file://$cache,https://proxy.golang.org" GONOSUMDB=github.com/ripmav/streamcrew \
  go build -o /dev/null . && echo "modulecheck: ok"
rm -rf "$(dirname "$cache")"
