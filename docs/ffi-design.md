# FFI: the design space, the decision, and the static-linking wall

This document records why GoScheme's foreign-function interface looks the way it
does, which alternatives were measured rather than assumed, and what would have
to change before a static binary could load shared libraries. It exists so that
the same ground does not have to be re-covered later.

## 1. The goal, and the constraint that fights it

The goal is ordinary: load a shared library at run time and call the C functions
in it, so that a Scheme program can reach anything the platform exposes.

The constraint is the project's existing promise: `dist/` ships one static
executable per platform, for six targets (linux, darwin, windows on amd64 and
arm64), cross-compiled from a single Linux machine with no platform SDKs and no
C cross-toolchains.

Those two pull in opposite directions. What follows is the measurement of how
far apart they really are.

## 2. "Call a C function" is three separate problems

Keeping these apart is what makes the rest of the document readable, because
different approaches solve different subsets.

**(a) Emitting the call.** A C call puts arguments in the platform's registers
and stack (System V AMD64: rdi, rsi, rdx, rcx, r8, r9, then the stack; doubles in
xmm0-7; the return in rax or xmm0), then jumps. Go's own convention is
different: registers per Go's ABI, a goroutine stack that grows and moves, a GC
that owns the heap. **Pure Go has no language construct for "call this address
with the C ABI".** Something has to generate that call — normally a C compiler,
via cgo.

**(b) Finding the function.** `dlopen`/`dlsym` on ELF platforms,
`LoadLibrary`/`GetProcAddress` on Windows. On ELF this is a libc service.

**(c) Running the callee properly.** C code wants its own stack (deep libc calls
need far more than a goroutine stack provides), and it expects `errno` and other
TLS state to be set up the C way. The runtime also has to know a thread is inside
C, or GC, preemption and signal handling will do the wrong thing.

## 3. The options, and what each actually costs

| # | Approach | (a) call | (b) address | (c) runtime | Cross-compiles without a C toolchain? | Static result? |
|---|---|---|---|---|---|---|
| O1 | cgo (shipped today) | C compiler | libc | cgo runtime | no | no, on ELF |
| O2 | C compiled to `.a`/`.syso` + Go asm stubs, `CGO_ENABLED=0` | asm | libc via dynamic import | *missing* | no (needs the C compiler regardless) | no |
| O3 | purego (a fake cgo runtime) | asm | `//go:cgo_import_dynamic` | self-built | yes | no, on ELF |
| O4 | hand-written trampolines (own fakecgo) | asm | as O3 | self-built | yes | no, on ELF |
| O5 | Windows `syscall.NewLazyDLL` only | stdlib | Win32 API | n/a (native) | yes | **yes** |
| O6 | statically link one chosen library at build time | C compiler | not needed | n/a | no | yes, but not general FFI |

### O1 — cgo (what the project ships)

`internal/scheme/ffi_cgo.go` is built only when cgo is on. It gives cgo the job
of (a) and (c) and calls `dlopen`/`dlsym` for (b). It works and is covered by
tests (174 assertions in the cgo flavour of the goscheme suite).

Costs: cgo needs a C compiler **for every target platform**, which is why the
released binaries are `CGO_ENABLED=0`; and on ELF platforms the result is a
dynamically linked binary.

### O2 — put the C in a `.a`, link it into a cgo-free binary

This was the most promising idea on paper: write the ABI shims in C, compile them
to an archive, and let the Go linker pull it in. It was measured, and the pieces
that work and the piece that does not are both informative.

Measured (`CGO_ENABLED=0`, `.build/aexp/`):

* A `.c` file compiled to `.o` and shipped as `*.syso` in the package directory
  **is** linked into the binary and **is** callable from a Go assembly stub:
  `answer() = 42`.
* A plain `.o` that references libc fails: `relocation target strlen not defined`.
* The same object inside an **`ar` archive** named `*.syso` links (with the
  linker printing `loadinternal: cannot find runtime/cgo`) and the produced
  binary gains `NEEDED libc.so.6`; a **shallow** libc call then works:
  `gsLen("hello") = 5`.
* A **deep** libc call — `dlopen` — **crashes**: the C code runs on the
  goroutine stack, the stack grows under it, and the unwinder meets a C return
  address:

  ```
  runtime: g 1: unexpected return pc for runtime.sigpanic
  fatal error: unknown caller pc
  ```

So (a) and (b) can be reached without cgo. (c) cannot: the fix is to run the
callee on the system stack, and in a non-cgo build the runtime disables exactly
that path:

```
fatal error: cgocall unavailable        (runtime.cgocall, runtime/cgocall.go)
-linkmode requires external (cgo) linking, but cgo is not enabled
```

Reimplementing (c) is what purego does, and it is not small: it vendors the Go
runtime's cgo support as `internal/fakecgo` and sets the runtime's own variables
through `//go:linkname`. And note that O2 needs a C compiler per target anyway,
so it does not even buy cross-compilation. **Rejected**: it pays O4's cost while
keeping O1's cross-compilation problem.

### O3 — purego

purego is a production library (Ebitengine) that calls C from Go without cgo. Its
`nocgo.go` explains its own machinery:

> if CGO_ENABLED=0 import fakecgo to setup the Cgo runtime correctly. … importing
> fakecgo will set these (using `//go:linkname`) with functions written entirely
> in Go (except for some assembly trampolines to change GCC ABI to Go ABI). Doing
> so makes it possible to build applications that call into C without
> CGO_ENABLED=1.

and its Linux `dlopen` binding is a Go directive, not a C file:

```go
//go:cgo_import_dynamic purego_dlopen dlopen "libdl.so.2"
```

Its main benefit is the one this project actually needs for macOS: *"No C means
you can build for other platforms easily without a C compiler."* It would let a
darwin FFI binary be cross-compiled from this Linux machine, which cgo cannot do
without an osxcross SDK.

Costs: a third-party dependency (Apache-2.0, with BSD-3 files copied from the Go
runtime, so a NOTICE and attribution are required), and a deliberate dependency
on Go runtime internals — `runtime.iscgo`, `_cgo_init`, `_cgo_thread_start`,
`//go:linkname` — which a future Go release may change. Version choice is
constrained too: purego v0.11.1 requires Go ≥ 1.25, so a project pinned to Go
1.22 would have to stay on the v0.8 line. And it still cannot produce a static
ELF binary that loads code, because of §4.

### O4 — the same thing, written here

Same reach as O3 without the dependency, at the price of maintaining per-arch
assembly and runtime-internals coupling in this repository. Rejected as the
default; worth revisiting only if a dependency is unacceptable.

### O5 — Windows, where the ideal actually holds

On Windows, loading a DLL is a system service (`LoadLibrary`/`GetProcAddress`),
not a dynamic-loader feature, and Go's standard library exposes it to ordinary Go
code (`syscall.NewLazyDLL`, `Proc.Find`, `Proc.Call`, `syscall.SyscallN`). No C
compiler, no cgo — so a **static `CGO_ENABLED=0` Windows binary can load a DLL at
run time**. This is the only platform in the set where "one static file that can
also call into native code" is available today.

The known weak point is floating point, which the Windows x64 ABI passes in
xmm0-3 while `Proc.Call` marshals every argument into integer registers; see §5.

### O6 — out of scope

Statically linking one specific library at build time is a real technique, but it
is not an FFI: it cannot load a library the user names at run time.

## 4. The wall: no static ELF binary can load code

This is the part that makes the original wish unreachable, and it is not a
limitation of Go, cgo, or this project — it is a property of ELF static linking.

`dlopen` is not a syscall. It is a service of the **dynamic loader** (`ld.so`,
`libc.so`). A statically linked program has no dynamic loader in the process, so
there is nothing to perform the load. Measured on this machine:

* **glibc 2.39**, `cgo` + `-extldflags "-static"`: `dlopen` "succeeds", but the
  linker says plainly what that means:

  > Using 'dlopen' in statically linked applications requires at runtime the
  > shared libraries from the glibc version used for linking

  It is a static binary that secretly needs matching glibc shared objects at run
  time — the worst of both. Worse, `dlopen("libc.so.6")` inside such a binary
  succeeds and puts a **second libc** in the process, so a pointer from one
  libc's `malloc` is freed by another's `free`.
* **musl 1.2.4**, the same build: it refuses outright, with and without
  `-Wl,-E`:

  ```
  dlopen(libm) failed: Dynamic loading not supported
  ```

  musl's author explains why, and why glibc's behaviour is not a counterexample:

  > This is expected to fail with "dynamic loading not supported" as the dlerror
  > error message. There is no dynamic loader for static linked programs. Doing
  > it is theoretically possible, but has a lot of limitations. glibc just
  > ignores these rather than even trying to do it right, leading to a situation
  > where it's unsafe if the glibc version available at runtime differs from the
  > one you static-linked with, among other things.
  > — Rich Felker, musl mailing list, 2021-09-24

**Conclusion: on ELF platforms, FFI implies dynamic linking.** Not because of
cgo, and not because of Go: because loading code at run time is the dynamic
loader's job, and a static program has none. Any approach (O1, O3, O4) that
reaches `dlopen` produces a dynamically linked binary. The choice is only about
*which* toolchain is needed, not about staticness.

## 5. Windows capability

Windows is the one platform where a *static, dependency-free, single-file* binary
can still call native code, because it does not go through a dynamic loader at
all: `LoadLibrary`/`GetProcAddress` are ordinary system services, and Go's
standard library exposes them to pure Go (`syscall.NewLazyDLL` →
`Proc.Find`/`Proc.Addr`/`Proc.Call`, and `syscall.SyscallN`). No cgo, no C
compiler, and `CGO_ENABLED=0` is fine — so the released Windows binaries could
gain this without giving up anything. (Note `syscall.NewLazySystemDLL` is *not*
in the standard library; it lives in `golang.org/x/sys/windows`.)

Floating point is the sharp edge, and Go's own source documents exactly where it
is sharp (`src/syscall/dll_windows.go`):

> On amd64, Call can pass and return floating-point values. To pass an argument x
> with C type "float", use uintptr(math.Float32bits(x)). To pass an argument with
> C type "double", use uintptr(math.Float64bits(x)). Floating-point return values
> are returned in r2.

The `amd64` scoping is not a hedge. The runtime's amd64 assembly mirrors each of
the first four arguments into both its integer register and its XMM register, and
copies XMM0 into `r2`; the arm64 assembly instead contains
`// TODO(rsc) floating point like amd64 in StdCallInfo_R2?`, never touches the FP
registers, and never writes `r2` at all. Go's own `TestFloatArgs`/`TestFloatReturn`
skip unless `GOARCH == "amd64"`, and windows/arm64 float arguments are an open
issue (golang/go#62583) whose symptom is a crash. So:

* windows/amd64: floating-point arguments in the **first four slots**, and float
  returns, work as raw bit patterns, with no type marshalling by the API;
* windows/arm64: integer, pointer and string arguments only, through `Proc.Call`.

An implementation therefore has to get several things right, and each one is a
trap rather than a detail: `Proc.Call`'s returned `error` is **always** non-nil
(decide success from `r1`, and a float return from `r2`); `Addr()`/`Call()` panic
if the symbol is missing, so call `Find()` first and turn its error into an
ordinary Scheme condition; nothing is marshalled, so strings go through
`syscall.UTF16PtrFromString` and floats through `math.Float64bits`; `uintptr`
arguments need `//go:uintptrescapes` discipline in the marshalling code; the
argument count is capped (42 in Go 1.22); and the standard library's `LoadDLL`
does not restrict the search path for arbitrary names, which is a DLL-preloading
hazard, so an untrusted library name should be resolved to an absolute path
first. Cgo-free static Windows builds rely on this same mechanism internally —
the runtime's own kernel32 imports use `//go:cgo_import_dynamic`, and the
standard library itself calls `syscall.NewLazyDLL` from `internal/syscall/windows`.

Not verifiable here: `wine` cannot start on this machine (`/run` is a read-only
mount and wine insists on creating `/run/user/$UID/wine`), and there is no
`qemu-user`. Every Windows statement above therefore rests on compilation plus
Go's documented behaviour, not on execution.

## 6. What is achievable today, measured

The ideal — one static file that also loads shared libraries — is out of reach on
ELF (§4), but everything short of it is not hypothetical. With purego (v0.8.0-alpha,
the newest release whose `go` directive, 1.18, still suits this project's pinned
Go 1.22.2 — v0.11.1 requires Go ≥ 1.25):

| target | FFI backend | cross-compiled here with no C toolchain | floating point | links statically |
|---|---|---|---|---|
| linux/amd64 | purego `Dlopen`/`RegisterLibFunc` | ✅ built **and run**: `cbrt(27.0) = 3.0000000000000004`, `strlen("hello") = 5` | ✅ (ran) | ❌ `NEEDED libdl.so.2, libc.so.6, libpthread.so.0` |
| linux/arm64 | purego | ✅ compiled | not measured here | ❌ |
| darwin/amd64 | purego | ✅ compiled, no osxcross | not measured here | ❌ |
| darwin/arm64 | purego | ✅ compiled, no osxcross | not measured here | ❌ |
| windows/amd64 | stdlib `NewLazyDLL` + `Proc.Call` | ✅ compiled | ✅ documented in Go's source, first four argument slots only | ✅ (native, no dynamic loader involved) |
| windows/arm64 | stdlib address + `purego.RegisterFunc` | ✅ compiled | ⚠️ unverified: `Proc.Call` cannot do it here at all (golang/go#62583), and whether purego's Windows trampolines assign the AArch64 FP registers is not established | ✅ |

Two practical notes for anyone picking this up:

* **The dependency must be vendored.** The default module cache here
  (`/home/xie/go/pkg/mod`) lies outside the writable area and is read-only, so
  `go get` fails on the cache and on the checksum database. `go mod vendor`
  writes into the repository and sidesteps both.
* **Licensing.** purego is Apache-2.0 (`LICENSE` at the module root; all 63 SPDX
  headers are Apache-2.0), but `internal/fakecgo` carries files from the Go
  runtime under BSD-3 (`// Copyright 2010 The Go Authors`). Compatible with this
  project's MIT, but it obliges us to ship their license texts and attribution.

## 7. The future: what would have to change

The block is upstream, not here, so the honest answer to "can this be done later"
is: watch these two things.

1. **A correct static `dlopen` in musl.** Rich Felker again: *"If we add it in
   some point in musl, it will need to be done in a way that doesn't have this
   problem"*, and *"There's a proposal that will make an easier way in the future
   too but it's not upstream yet."* A static binary that links musl and gets a
   real, self-contained loader would let a genuinely static FFI binary exist on
   Linux. Nothing to build until that lands; nothing here blocks it.
2. **Nothing else needs to wait.** The C-toolchain problem is already solved by
   the table in §6: cgo-free FFI cross-compiles to all six targets from this
   machine. What is left is a decision, not a research question.

Interim position, and what the project does today:

* keep the released binaries static and dependency-free (the property most users
  actually need);
* keep the cgo-gated FFI for people who build with `CGO_ENABLED=1`, since it
  works and is tested;
* treat "static binary that also loads shared libraries" as **not achievable on
  ELF today**, and stop spending effort on it;
* the cheapest real win, if FFI matters more than the last bit of portability, is
  the windows/amd64 standard-library path in §5: it costs no dependency and
  keeps that binary static.

## 8. Reproducing the measurements

Everything above was measured in this repository's `.build/` scratch area; the
commands are short enough to repeat. A C shim compiled to an archive and dropped
in a package as `shim.syso`, built with `CGO_ENABLED=0`, reproduces both the
`gsLen` success and the `dlopen` crash. The static `dlopen` results need
`CGO_ENABLED=1` plus `-ldflags '-linkmode external -extldflags "-static"'`, once
with the default gcc and once with `CC=musl-gcc`.

Environment note: this machine has no `qemu-user`, and `wine` cannot start
because `/run` is a read-only mount, so non-Linux/amd64 binaries can be compiled
but not executed here. Claims about them are limited to compilation, `go vet`,
and the platform documentation cited above.
