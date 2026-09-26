#!/bin/sh
# Cross compile GoScheme for every supported platform into dist/.
#
# Targets: windows, linux and macOS on amd64 and arm64.
set -eu

GO=${GO:-/usr/bin/go}
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST="$ROOT/dist"

platforms="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"
VERSION=$(cat "$ROOT/VERSION")
LDFLAGS="-s -w -X main.version=$VERSION"

mkdir -p "$DIST"
cd "$ROOT"

for p in $platforms; do
    os=${p%%/*}
    arch=${p##*/}
    out="$DIST/goscheme-$os-$arch"
    [ "$os" = "windows" ] && out="$out.exe"
    printf 'building %s ... ' "$(basename "$out")"
    GOOS=$os GOARCH=$arch CGO_ENABLED=0 "$GO" build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/goscheme
    printf 'ok\n'
done

# Remove the old manifest first: otherwise the shell truncates it before
# sha256sum reads it, and the file ends up hashing itself.
( cd "$DIST" && rm -f SHA256SUMS && sha256sum ./* > SHA256SUMS )
echo
echo "artifacts in $DIST:"
ls -l "$DIST"
