# (goscheme ffi)

The foreign-function interface loads a shared library at run time and calls the
C functions it exports.  It is built on cgo, which emits the C calling
convention and runs the callee on a proper C stack, and on the platform's
dynamic loader (`dlopen`/`dlsym` on POSIX, `LoadLibrary`/`GetProcAddress` on
Windows) to open the library and find the symbol.  **It exists only in cgo
(`CGO_ENABLED=1`) builds**: the default `CGO_ENABLED=0` binaries are static and
do not include it, and only a cgo build reports `ffi` in `(features)`.

```scheme
(import (scheme base) (goscheme ffi))
```

## Procedures

### Loading libraries and functions

| Procedure | Arguments | Description |
|---|---|---|
| `load-shared-library` | `(load-shared-library name)` | Opens a shared library and returns a foreign library object.  `name` is a string holding a soname, a file name or a path, or `#f` (an empty string behaves the same way) for the running program's own symbols, which is where libc lives on POSIX.  On POSIX the call is `dlopen(name, RTLD_NOW \| RTLD_GLOBAL)`; on Windows it is `LoadLibraryA`.  Failure raises a file error that carries the loader's own message. |
| `foreign-library?` | `(foreign-library? obj)` | `#t` when `obj` is a foreign library object returned by `load-shared-library`, `#f` for every other value. |
| `foreign-function` | `(foreign-function library name return-type arg-type ...)` | Resolves the symbol `name` (a symbol such as `'strlen`) in `library` and returns a new procedure with one parameter per `arg-type`.  The symbol is looked up here, so a missing symbol raises at this call rather than when the returned procedure runs.  `return-type` and each `arg-type` are symbols from the vocabulary below, and the returned procedure is named after `name` in error messages. |

## Type vocabulary

Each type is named by a symbol, written in the call as `'double`, `'pointer`,
and so on.

* **`void`** — allowed only as a return type; as an argument it raises
  `void is not a valid argument type`.  There is no value to deliver, so treat
  whatever comes back as unspecified: the implementation reads it as an
  integer, and a C function that really returns `double` therefore yields a
  meaningless number.
* **`int`, `long`, `size_t`, `ssize_t`** — one 64-bit integral type.  As an
  argument it takes an exact integer; as a result it produces an exact integer.
  All four names mean the same thing, and the shim passes `int64_t` rather than
  C `long`, which is only 32 bits on Windows.
* **`double`** — as an argument it takes a real number (an exact integer is
  converted); as a result it produces a flonum.
* **`string`, `char*`** — a C `char *`.  As an argument it takes a Scheme
  string, which is copied into a NUL-terminated C buffer that lives only for
  the duration of the call; as a result it produces a fresh Scheme string
  copied out of the returned C string, or `#f` when the pointer is NULL.
* **`pointer`, `void*`** — a raw address.  As an argument it takes an exact
  integer address, typically one an earlier call returned; as a result it
  produces an exact integer address, or `#f` when the pointer is NULL.

An unrecognised type name raises `unknown foreign type <name>`.

## Calling convention

* Arguments must be either all integral (`int`/`long`/`size_t`/`ssize_t`,
  `string`/`char*`, `pointer`/`void*`) or all `double`; mixing the two raises
  when `foreign-function` is called.  At most four integral or three double
  arguments are accepted, and the call produces a single value.
* Any return type may be declared with either argument class, but declare the
  C function's real return type: a mismatch is not converted, it reinterprets
  the raw return register, so declaring `long` for a function that returns
  `double` gives the bit pattern of the double and vice versa.  `int` with
  double arguments is correct for a function such as `lround`, which really
  returns a long.
* The returned procedure takes exactly the declared number of arguments;
  calling it with any other count is the interpreter's usual
  wrong-number-of-arguments error.

## Notes

* Errors are ordinary conditions.  A missing library is a file error raised by
  `load-shared-library` (the message includes the loader's own text); a missing
  symbol is an error raised by `foreign-function` naming the symbol and the
  library; a wrong argument type or count is an error raised when the returned
  procedure is called, prefixed with the symbol's name, as in
  `strlen: argument 1 should be a string but is 5`.
* `#f` as the library name means the running program's own symbols.  On ELF
  hosts that is where libc lives, which is how `strlen` and `getenv` are
  reached; on Windows it is the running executable, which does not export the C
  runtime, so name a DLL instead — `ucrtbase.dll` for the string and math
  functions, `kernel32.dll` for the Windows API.
* A `string` argument is copied for the call only, so a pointer a function
  returns *into one of its arguments* (as `strchr` does) is dangling once the
  call returns, while a pointer into memory C owns (as `getenv` returns) stays
  valid.  A `pointer` result is only an address: the interpreter does not track
  its size or lifetime, so reading or writing through it needs another foreign
  call and a mistake is a crash rather than a Scheme error.
* There are no structs, callbacks, variadic functions or by-reference
  arguments; anything beyond an integer, a pointer, a string or a double
  belongs behind a small C shim.
* `scripts/build-dist.sh` builds the dynamic flavour as
  `goscheme-<os>-<arch>-dynamic` beside the static binaries — by default for
  linux/amd64, linux/arm64 and windows/amd64, with the CI workflow adding
  macOS and Windows — and that is the flavour with this library.  A static
  `CGO_ENABLED=0` binary still registers the three names, but every call raises
  with a message saying it was built without cgo and to rebuild with
  `CGO_ENABLED=1`; `foreign-library?` raises there too rather than returning
  `#f`.
* The dynamic Linux binaries are linked against the C library and need it at
  run time; the ones built here were built against glibc 2.34, so they need a
  glibc at least that new.  On ELF platforms FFI and a truly static binary
  cannot both be had, because `dlopen` is a service of the dynamic loader and a
  static program has none; [docs/ffi-design.md](../ffi-design.md) records the
  measurements behind that and what would have to change upstream.

## Example

**Requires the dynamic (cgo) build.**  The released `dist/` binaries are static
`CGO_ENABLED=0` builds and do not include this library, so this example does
not run on them; it needs a `-dynamic` binary or any build made with
`CGO_ENABLED=1` (the repository's cgo build, which is what the listing below
was checked against, prints `5` and `3.0000000000000004`).  The library names
differ per platform: `libm.so.6` on Linux, `libSystem.B.dylib` on macOS,
`ucrtbase.dll` on Windows.

```scheme
(import (scheme base) (scheme write) (goscheme ffi))

;; #f opens the running program's own symbols, which is where libc lives.
(define libc (load-shared-library #f))
(define strlen (foreign-function libc 'strlen 'long 'string))
(display (strlen "hello"))                     ; => 5
(newline)

;; A named library: libm on Linux, libSystem.B.dylib on macOS.
(define libm (load-shared-library "libm.so.6"))
(define cbrt (foreign-function libm 'cbrt 'double 'double))
(display (cbrt 27.0))                          ; => 3.0000000000000004
(newline)
```
