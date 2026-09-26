# GoScheme

A complete **R7RS Scheme** interpreter written from scratch in **Go**, with no
third-party dependencies.

**English** | [简体中文](README_zh.md)

```
$ goscheme -e '(display (map (lambda (x) (* x x)) (list 1 2 3 4))) (newline)'
(1 4 9 16)
```

* **Evaluation core** — a CEK-style abstract machine with an explicit
  continuation stack. Procedure calls never push a return frame, so **proper
  tail calls** are structural rather than simulated, and `call/cc` is
  implemented by copying the stack, which makes continuations **multi-shot**.
* **Macros** — hygienic `syntax-rules`: nested ellipses, tail patterns after an
  ellipsis, custom ellipsis identifiers, ellipsis escape `(... template)`, and
  referential transparency for template-introduced identifiers.
* **Numeric tower** — exact integers (`int64` with automatic promotion to
  `big.Int`), exact rationals, `float64`, and complex numbers, with full R7RS
  exactness contagion and transitive comparisons.
* **Libraries** — `define-library` / `import` with `only`, `except`, `prefix`
  and `rename`, plus every R7RS-small `(scheme …)` library.
* **Conformance** — the reference R7RS test suite
  ([chibi-scheme `r7rs-tests.scm`](test/scheme/r7rs-tests.scm)) passes in full:
  **1227 assertions, 0 failures**.

## Table of contents

- [Quick start](#quick-start)
- [Command line](#command-line)
- [Repository layout](#repository-layout)
- [Language coverage](#language-coverage)
- [Implementation notes](#implementation-notes)
- [Testing](#testing)
- [Cross-compilation](#cross-compilation)
- [Requirements](#requirements)
- [Known limitations](#known-limitations)

## Quick start

```sh
git clone https://github.com/MrXie1109/GoScheme.git && cd GoScheme

make build          # or: go build -o .build/goscheme ./cmd/goscheme
make test           # Go unit tests + both Scheme test suites
make dist           # cross-compile every supported platform into dist/
```

Run a program, evaluate an expression, or start a REPL:

```sh
./.build/goscheme program.scm                 # run a file
./.build/goscheme -e '(display (+ 1 2))'      # evaluate an expression
./.build/goscheme                             # interactive REPL
./.build/goscheme -i program.scm              # load, then REPL
```

## Command line

```
goscheme [options] [file] [argument ...]

  -e, --eval EXPR     evaluate EXPR (may be repeated, evaluated in order)
  -i, --interactive   enter the REPL after loading FILE
  -q, --quiet         do not print the REPL banner
  -v, --version       print the version and exit
  -h, --help          print usage
  --                  end of options; the next argument is the script
```

With neither a file nor `-e`, the interpreter starts a REPL. `(command-line)`
returns the program name, the script and its arguments.

Exit status: `0` on success, the argument of `(exit n)`, `1` for `(exit #f)` or
for an uncaught error.

## Repository layout

```
cmd/goscheme/main.go      command line driver (file / -e / REPL)
internal/scheme/          the interpreter
  value.go                runtime objects (symbols, pairs, strings, vectors,
                          bytevectors, procedures, records, …)
  number.go               numeric tower and arithmetic
  reader.go               lexer and datum reader
  printer.go              write / display / write-shared / write-simple
  env.go                  lexical environments and hygienic name resolution
  machine.go              CEK machine, continuations, dynamic-wind, exceptions
  eval.go                 special forms and derived syntax
  macro.go                syntax-rules pattern matching and instantiation
  equal.go                eq? / eqv? / equal?
  library.go              R7RS libraries and import sets
  port.go                 textual, binary, string and bytevector ports
  builtins.go             procedure registration and argument checking
  b_number.go             numeric procedures
  b_list.go               pairs and lists
  b_string.go             strings, characters, symbols
  b_vector.go             vectors and bytevectors
  b_control.go            apply, map, continuations, values, promises
  b_io.go                 ports, read and write
  b_system.go             files, process context, time, eval and load
  b_hashtable.go          hash tables (extension)
  scheme_test.go          Go unit tests and suite drivers
test/scheme/              Scheme level tests
  r7rs-tests.scm          the reference R7RS test suite
  goscheme-tests.scm      regression tests specific to this implementation
  chibi/test.scm          (chibi test) compatibility shim used by the suites
  run-r7rs.scm            drivers: goscheme run-r7rs.scm
  run-goscheme.scm
dist/                     cross-compiled release binaries
scripts/build-dist.sh     cross-compilation script used by `make dist`
Makefile                  build, test and dist targets
```

## Language coverage

### Syntax

`quote` `quasiquote` `unquote` `unquote-splicing` `if` `define` `set!` `lambda`
`case-lambda` `begin` `let` `let*` `letrec` `letrec*` `let-values`
`let*-values` `define-values` `cond` `case` `and` `or` `when` `unless` `do`
`delay` `delay-force` `parameterize` `guard` `define-record-type`
`define-syntax` `let-syntax` `letrec-syntax` `syntax-rules` `include`
`include-ci` `cond-expand` `import` `define-library`

(`else` and `=>` are recognised as auxiliary syntax only when they are not
shadowed by a variable binding, as the report requires.)

The reader accepts the complete R7RS lexical syntax: block comments `#|…|#`
(nestable), datum comments `#;`, `#!fold-case` / `#!no-fold-case`, vectors
`#(…)`, bytevectors `#u8(…)`, datum labels `#0=` / `#0#` (including cycles),
`|…|` symbols with escapes, all character names plus `#\xHH`, string escapes
with intraline line continuation, and the full numeric grammar with any
combination of `#b #o #d #x` and `#e #i` prefixes.

### Libraries

`(scheme base)` `(scheme case-lambda)` `(scheme char)` `(scheme complex)`
`(scheme cxr)` `(scheme eval)` `(scheme file)` `(scheme inexact)`
`(scheme lazy)` `(scheme load)` `(scheme process-context)` `(scheme read)`
`(scheme repl)` `(scheme time)` `(scheme write)` `(scheme r5rs)`

plus one extension library, `(goscheme hash-table)`.

### Data types

Booleans; numbers (exact integers, exact rationals, inexact reals, complex);
characters with full Unicode case mapping; mutable strings; symbols; pairs and
lists; vectors; bytevectors; procedures (closures, primitives, continuations,
parameter objects); promises; records; errors; ports; environments; `eof`; the
unspecified value; hash tables (extension).

### Procedures

323 bindings are installed in the standard environment (about 250 of them are
the R7RS-small procedures), and the builtin libraries export 572 names in
total. Coverage includes the numeric tower (`exact-integer-sqrt`,
`rationalize`, `floor/`, `truncate/`, `make-polar`, `number->string` with any
radix, …), list and vector operations, Unicode-aware string and character
operations, textual and binary I/O, file and process-context procedures,
`eval` / `load` / `environment`, `dynamic-wind`, `guard`,
`with-exception-handler`, `parameterize`, promises and `values`.

## Implementation notes

### Proper tail calls

The machine keeps its continuation as an explicit stack of frames. Evaluating a
combination pushes frames only for the operand expressions that still have to
be evaluated; **applying a procedure pushes nothing**. The callee therefore
runs with the caller's continuation, which is exactly a tail call. Tail
position is preserved through `if`, `cond`, `case`, `and`, `or`, `when`,
`begin`, `let`/`let*`/`letrec` bodies, `apply`, `call-with-values`, `force`,
`dynamic-wind`, macro expansion and `guard`.

```sh
$ goscheme -e '(let loop ((i 0)) (if (= i 2000000) i (loop (+ i 1))))'
2000000        # constant stack space
```

Deep *non-tail* recursion also works: continuation frames live on the heap, not
on the Go call stack.

### First-class continuations

`call/cc` captures the continuation stack together with the `dynamic-wind` wind
stack and the exception-handler stack, and copies them, so a captured
continuation may be resumed any number of times. Resuming computes the longest
common prefix of the current and target wind stacks, runs the `after` thunks of
the frames being left (innermost first) and the `before` thunks of the frames
being entered, then reinstates the state. `guard` and `with-exception-handler`
use the same transition, so unwinding out of nested `dynamic-wind` forms runs
every `after` thunk exactly once and in the right order.

### Hygienic macros

Identifiers introduced by a template carry a mark that records the environment
of the macro definition. Name resolution first searches the lexical
environment (so a binding introduced by an expansion never captures a user
binding, and vice versa) and then falls back through the mark chain, which
provides referential transparency. Marks compose, so macros that expand into
macro definitions stay hygienic. Literals are compared by identifier identity
or by binding, not merely by name, which is what R7RS 4.3.2 requires.

### Errors and conditions

Errors raised by primitives and by `error`/`raise` are dispatched to the
innermost handler. `raise` is non-continuable (a returning handler triggers a
secondary exception), while `raise-continuable` reinstates the handler stack
and continues at the `raise` point. `error-object?`, `error-object-message`,
`error-object-irritants`, `read-error?` and `file-error?` are all supported.

### Numeric tower

Exact integers stay in a machine word until they overflow and then promote
automatically to `big.Int`; exact rationals use `big.Rat` and are always kept
in lowest terms with integral values normalised back to integers. Comparisons
between an exact and an inexact operand convert the inexact operand to exact
first, which keeps `=`, `<`, … transitive as R7RS 6.2.6 recommends.

### Extensions

Beyond R7RS-small the interpreter also provides:

* `(goscheme hash-table)` — `make-eq-hashtable`, `make-eqv-hashtable`,
  `make-equal-hashtable`, `hash-table-ref`, `hash-table-ref/default`,
  `hash-table-set!`, `hash-table-update!`, `hash-table-delete!`,
  `hash-table-exists?`, `hash-table-keys`, `hash-table-values`,
  `hash-table-walk`, `hash-table->alist`, `alist->hash-table`,
  `hash-table-copy`, `hash-table-clear!`, `hash-table-size`, `hash-table-count`
  and `hash`.
* `(assert expr)`, `#!unspecified`, and the alternative exponent markers
  `s f d l` accepted by the reader.

## Testing

```sh
make test                                     # everything
go test ./...                                 # same
go test -short ./...                          # skip the reference suite
./.build/goscheme test/scheme/run-r7rs.scm    # reference suite only
./.build/goscheme test/scheme/run-goscheme.scm
```

```
== 1227 passed, 0 failed     reference R7RS suite (test/scheme/r7rs-tests.scm)
==  135 passed, 0 failed     GoScheme regression suite (test/scheme/goscheme-tests.scm)
```

* `r7rs-tests.scm` is the suite maintained by chibi-scheme. It exercises
  sections 4.1–4.3 (primitive, derived and macro syntax) and 6.1–6.14 (all
  standard procedures), including hygiene corner cases, the numeric grammar,
  reader syntax and cyclic-output handling. It is run through the bundled
  `(chibi test)` shim.
* `goscheme-tests.scm` adds regression coverage for proper tail calls,
  multi-shot continuations, `dynamic-wind` re-entry and unwinding, library
  import transformations, records, textual/binary ports, file round-trips,
  `include` / `cond-expand`, hash tables, exceptions and `eval` / `load`.
* The Go tests in `internal/scheme/scheme_test.go` drive both suites and also
  contain direct unit tests for the reader, the numeric tower, tail-call
  behaviour and error propagation.

## Cross-compilation

`make dist` (or `scripts/build-dist.sh`) builds statically linked binaries for
every supported target into `dist/`. No cgo, no external toolchain.

| Artifact | Platform |
|---|---|
| `goscheme-linux-amd64` | Linux x86-64 |
| `goscheme-linux-arm64` | Linux AArch64 |
| `goscheme-darwin-amd64` | macOS Intel |
| `goscheme-darwin-arm64` | macOS Apple Silicon |
| `goscheme-windows-amd64.exe` | Windows x86-64 |
| `goscheme-windows-arm64.exe` | Windows on ARM |

Build flags: `GOOS=<os> GOARCH=<arch> CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`.
`dist/SHA256SUMS` records the checksum of every artifact.

## Requirements

* Go 1.22 or newer (the module targets `go 1.22`).
* No third-party modules — the standard library only.
* The interpreter itself runs on Linux, macOS and Windows, on amd64 and arm64.

## Known limitations

* `define-syntax` supports `syntax-rules` transformers only, which is the full
  extent of the R7RS-small macro system.
* The target language is R7RS-small; R7RS-large and the SRFI libraries are not
  provided, apart from the bundled hash-table extension.
* It is a tree-walking interpreter — there is no compiler or JIT. Tail calls
  are proper, but deep non-tail recursion allocates heap frames.
* Inexact numbers are printed with Go's shortest round-trip representation, and
  symbols that merely look like numbers (for example `+NaN.0abc`) are quoted
  with `|…|` by `write`.
* `write-simple` on cyclic data may not terminate, which the report permits.
