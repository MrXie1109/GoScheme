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

## The native compiler

`goscheme compile` emits LLVM IR, runs `opt`, runs `llc`, and links the object
against the runtime archive built from `re/` — so testing the native path needs a
toolchain the Go tests are not allowed to require, and it lives in two Makefile
targets rather than in `go test`:

```sh
make check-compile     # compile a program, run it, diff against the interpreter
make check-llvm        # stop after the IR and assemble it with llvm-as
```

`check-compile` is the one that matters, and the comparison is the whole point:
a generated module can be well-formed, verify cleanly, and still compute
something else — or be dead code that nothing ever calls, which is a failure mode
that inspecting the IR cannot catch. What is checked is the output of a program
that ran.

What that needs installed is `opt` and `llc` (the LLVM tools, on `PATH`) and a C
compiler (`cc`, or `gcc` on Windows); `make check-llvm` also wants `llvm-as`.
None of them is a Go test dependency, so a checkout without them still runs
`make test` in full — it is only these two targets that fail, with the
toolchain's own message naming the missing program. The runtime archive is built
on demand with `go build -buildmode=c-archive`, which needs **cgo**, and it is
cached under the user's cache directory keyed to the interpreter's own build, so
a stale archive is never linked against a newer compiler.

The compiler only targets the host, which is why it is not wired into `make dist`:
the six release binaries are the *interpreter*, cross-compiled as before, and a
program that wants machine code is compiled on the machine it will run on. See
[docs/compile.md](compile.md) for what it accepts and refuses.

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
* `internal/scheme/compile.go` and `vm.go` are the bytecode compiler and the VM
  that runs a file; `ir.go` and `ir_pure.go` are the LLVM generator and the
  pure-body scan; `pack.go` is the packed-source round trip; `re.go` builds and
  finds the runtime archive.
* `re/` — the GoScheme Runtime Environment, compiled to a C archive that a
  native program links against.  It is a separate package because
  `-buildmode=c-archive` needs a `main`, and because the boundary between "the
  runtime a compiled program links" and "the interpreter this process runs" is
  worth having a name.
* `bench/` — the C, Python and Guile counterparts of the twelve workloads, plus
  `run.sh` (interpreter against them) and `run-native.sh` (compiler against
  interpreter).
* `test/scheme/` — one suite per area plus its `run-*.scm` driver; a suite is
  registered in `internal/scheme/scheme_test.go`.
* `docs/extensions/`, `docs/srfi/` — one reference page per library; a test
  fails if a library exports a name its page does not mention.
* `docs/compile.md` — the native compiler's design and limits, written down
  because a limit that is not documented is a bug report waiting to happen.
* `docs/manual/` — the bilingual guide.

When adding a library: write the Go or embedded-Scheme source, register it in
`installBuiltins` or with `registerInstaller`, add a suite, add its page, and
run `go test ./internal/scheme/ -run TestExtensionDocsCoverEveryExport`.
