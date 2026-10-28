#!/usr/bin/env bash
# Copies the release-publish Go SDK (pkg/relclient, pkg/relproto) into core/third_party/relhub,
# so the public repo and CI build without the local ~/dev/release-publish checkout.
# The copy is never edited by hand: change the SDK in release-publish, then re-run this.
#
#   scripts/sync-relhub.sh [RELEASE_PUBLISH_DIR]      default: ~/dev/release-publish
#
# The SDK's own tests use the server's internal packages, so only the sources are copied;
# core/update has its own tests against a fake server and (when `relhub` is installed) the real one.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
src="${1:-$HOME/dev/release-publish}"
dst="$root/core/third_party/relhub"

[ -f "$src/pkg/relclient/client.go" ] || { echo "not a release-publish checkout: $src" >&2; exit 1; }
if [ -n "$(git -C "$src" status --porcelain -- pkg go.mod)" ]; then
  echo "warning: $src has uncommitted SDK changes; REVISION will say so" >&2
  dirty=" (+ uncommitted changes)"
else
  dirty=""
fi

rm -rf "$dst/pkg"
mkdir -p "$dst/pkg"
for p in relclient relproto; do
  mkdir -p "$dst/pkg/$p"
  for f in "$src/pkg/$p/"*.go; do
    case "$f" in *_test.go) continue ;; esac
    cp "$f" "$dst/pkg/$p/"
  done
done
cp "$src/go.mod" "$dst/go.mod"
printf '%s%s\n' "$(git -C "$src" rev-parse HEAD)" "$dirty" > "$dst/REVISION"
echo "synced $(git -C "$src" rev-parse --short HEAD)$dirty -> core/third_party/relhub"
echo "next: (cd core && go build ./... && go test -race ./update/...)"
