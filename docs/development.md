# Development notes

Everything a contributor needs that is not in the main README.  The short
version: `make build`, `make test`, `make dist`.

## Building and testing

```sh
export GOCACHE=$PWD/.gocache      # keep the build cache inside the checkout
make build                        # .build/goscheme
make test                         # the Go tests and every Scheme suite
go test -short ./...              # skip the reference R7RS suite
./.build/goscheme test/scheme/run-srfi-1.scm      # one suite on its own
```

`.build/` and `.gocache/` are ignored by git and exist to be deleted: after a
few releases they hold several gigabytes of binaries, checkouts and cross
toolchains.  `rm -rf .gocache .build/*` is always safe; `make build` puts back
what you need.

The reference suite needs the chibi-scheme test shim, which is in the
repository (`test/scheme/chibi/test.scm`), so no network is required.

## Releasing

**CI does it.**  Pushing a `v*` tag runs the test matrix on five native runners
and then the `release` job, which builds the six static binaries, runs one of
them (version and two suites) and attaches them to the GitHub release — see
`.github/release-notes/README.md`.  There is nothing to do locally, and no
cross toolchain anywhere: the platforms are exercised on their own runners
rather than under an emulator.

`make dist` is still the same build, for looking at the artifacts or for a
release that has to be made by hand:

```sh
make dist                                  # six static binaries into dist/
DYNAMIC_PLATFORMS= make dist               # ... and nothing else (releases)
DYNAMIC_PLATFORMS="linux/amd64" scripts/build-dist.sh   # one dynamic flavour
```

The static binaries are `CGO_ENABLED=0`, so they have every library except
`(goscheme ffi)`.  A dynamic build needs a C compiler for the target and is the
only flavour with FFI; the darwin ones are built on a real Mac by
`.github/workflows/ci.yml`, because they need an osxcross SDK.

The binaries are shipped as they are built — no packer — which keeps `make dist`
down to a couple of seconds with a warm cache.

## Local cross toolchains (optional)

CI covers every platform, so this is only for debugging a foreign binary in a
local checkout.  All three can be unpacked instead of installed, and all three
are regenerable, so delete them whenever the checkout gets fat.

```sh
# arm64 Linux, under emulation
apt-get download qemu-user-static
dpkg-deb -x qemu-user-static_*.deb .build/tools/qemu
.build/tools/qemu/usr/bin/qemu-aarch64-static ./dist/goscheme-linux-arm64 --version

# Windows amd64, under wine (the prefix is ~1.4G; delete it when done)
WINEPREFIX=$PWD/.build/wineprefix wine ./dist/goscheme-windows-amd64.exe prog.scm

# a C compiler for the dynamic Windows build, extracted the same way
apt-get download gcc-mingw-w64-x86-64-posix gcc-mingw-w64-x86-64-posix-win32 \
                 gcc-mingw-w64-base mingw-w64-x86-64-dev mingw-w64-common
dpkg-deb -x ./*.deb .build/tools/mingw
CC_windows_amd64=$PWD/.build/tools/mingw/usr/bin/x86_64-w64-mingw32-gcc-posix \
  DYNAMIC_PLATFORMS="windows/amd64" scripts/build-dist.sh
```

macOS cannot be run here at all; the darwin binaries are exercised by CI on a
real runner.

## Where things live

* `internal/scheme/` — the interpreter, one file per area, and `b_*.go` for the
  libraries.  `b_srfi*.go` are the SRFI libraries, `b_fast*.go` is
  `(goscheme fast)`.
* `test/scheme/` — one suite per area plus its `run-*.scm` driver; a suite is
  registered in `internal/scheme/scheme_test.go`.
* `docs/extensions/`, `docs/srfi/` — one reference page per library; a test
  fails if a library exports a name its page does not mention.
* `docs/manual/` — the bilingual guide.

When adding a library: write the Go or embedded-Scheme source, register it in
`installBuiltins` or with `registerInstaller`, add a suite, add its page, and
run `go test ./internal/scheme/ -run TestExtensionDocsCoverEveryExport`.
