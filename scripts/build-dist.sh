#!/bin/sh
# SPDX-License-Identifier: MIT
# Build GoScheme for every supported platform into dist/, in both flavours.
#
#   static   CGO_ENABLED=0: no dependencies at run time, and no FFI, so
#            (features) does not report `ffi`.  This is the one to ship when
#            portability matters most.
#   dynamic  CGO_ENABLED=1: linked against the target's C library, and the only
#            flavour that can load shared libraries, so it is the one with
#            (goscheme ffi).  It needs that C library at run time.
#
# A dynamic build needs a C compiler for the target platform, so the platforms
# that get one are listed in DYNAMIC_PLATFORMS; anything without a compiler is
# skipped with a message rather than failing the whole run.  Override the list
# to build fewer, or more if you have the cross compilers:
#
#   DYNAMIC_PLATFORMS="linux/amd64" scripts/build-dist.sh
#   CC_windows_amd64=x86_64-w64-mingw32-gcc scripts/build-dist.sh
set -eu

GO=${GO:-/usr/bin/go}
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST="$ROOT/dist"

platforms="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"
DYNAMIC_PLATFORMS=${DYNAMIC_PLATFORMS:-linux/amd64 linux/arm64}
VERSION=$(cat "$ROOT/cmd/goscheme/VERSION")
LDFLAGS="-s -w -X main.version=$VERSION"
GOFLAGS=${GOFLAGS:--trimpath}

# The C compiler for a dynamic build of each platform.  The defaults are the
# names the usual cross toolchains install as; override with CC_<os>_<arch>.
cc_for() {
    case $1 in
    linux/amd64) echo "${CC_linux_amd64:-cc}" ;;
    linux/arm64) echo "${CC_linux_arm64:-aarch64-linux-gnu-gcc}" ;;
    darwin/amd64) echo "${CC_darwin_amd64:-o64-clang}" ;;
    darwin/arm64) echo "${CC_darwin_arm64:-oa64-clang}" ;;
    windows/amd64) echo "${CC_windows_amd64:-x86_64-w64-mingw32-gcc}" ;;
    windows/arm64) echo "${CC_windows_arm64:-aarch64-w64-mingw32-gcc}" ;;
    *) echo "${CC:-cc}" ;;
    esac
}

mkdir -p "$DIST"
cd "$ROOT"

# Remove previous artifacts, so that dropping a platform from the lists cannot
# leave a stale binary behind for the next release.
rm -f "$DIST"/goscheme-* "$DIST"/SHA256SUMS

skipped=""
for p in $platforms; do
    os=${p%%/*}
    arch=${p##*/}
    suffix=""
    [ "$os" = "windows" ] && suffix=".exe"

    out="$DIST/goscheme-$os-$arch$suffix"
    printf 'building %-36s ' "$(basename "$out")"
    GOOS=$os GOARCH=$arch CGO_ENABLED=0 "$GO" build $GOFLAGS -ldflags "$LDFLAGS" -o "$out" ./cmd/goscheme
    printf 'ok\n'

    case " $DYNAMIC_PLATFORMS " in
    *" $p "*)
        cc=$(cc_for "$p")
        if ! command -v "$cc" >/dev/null 2>&1; then
            skipped="$skipped $p($cc)"
            continue
        fi
        out="$DIST/goscheme-$os-$arch-dynamic$suffix"
        printf 'building %-36s ' "$(basename "$out")"
        GOOS=$os GOARCH=$arch CGO_ENABLED=1 CC=$cc "$GO" build $GOFLAGS -ldflags "$LDFLAGS" -o "$out" ./cmd/goscheme
        printf 'ok\n'
        ;;
    esac
done

# Remove the old manifest first: otherwise the shell truncates it before
# sha256sum reads it, and the file ends up hashing itself.
( cd "$DIST" && rm -f SHA256SUMS && sha256sum ./* > SHA256SUMS )

echo
if [ -n "$skipped" ]; then
    echo "skipped dynamic builds (no C compiler):$skipped" >&2
    echo "set the matching CC_<os>_<arch>, or narrow DYNAMIC_PLATFORMS, to change that" >&2
    echo >&2
fi
echo "artifacts in $DIST:"
ls -l "$DIST"
