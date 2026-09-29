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
- [Standalone executables](#standalone-executables)
- [Repository layout](#repository-layout)
- [Language coverage](#language-coverage)
- [Implementation notes](#implementation-notes)
- [Testing](#testing)
- [Cross-compilation](#cross-compilation)
- [Requirements](#requirements)
- [Known limitations](#known-limitations)
- [License](#license)

## Quick start

```sh
git clone https://github.com/MrXie1109/GoScheme.git && cd GoScheme

make build          # or: go build -o .build/goscheme ./cmd/goscheme
make test           # Go unit tests + both Scheme test suites
make dist           # cross-compile every supported platform into dist/

./.build/goscheme -v   # GoScheme 1.2.1 (R7RS)
```

The version comes from `cmd/goscheme/VERSION`, which is embedded in the binary,
so even a plain `go build` reports it; `make dist` additionally stamps it with
`-ldflags "-X main.version=..."`.

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
goscheme build <script> [-o <output>] [-i <interpreter>]

  -e, --eval EXPR     evaluate EXPR (may be repeated, evaluated in order)
  -i, --interactive   enter the REPL after loading FILE
  -q, --quiet         do not print the REPL banner
  -v, --version       print the version and exit
  -h, --help          print usage
  --                  end of options; the next argument is the script
```

With neither a file nor `-e`, the interpreter starts a REPL.  The primary
prompt is `>>> `, and `... ` appears while a form is still open.

On a terminal the REPL switches the terminal into raw mode and provides the
editing keys one expects: cursor movement (arrows, Home/End, Ctrl-A/E/B/F),
backspace and delete, Ctrl-U/K/W, Ctrl-L to clear the screen, Ctrl-C to abandon
the line, Ctrl-D to leave, and history via the up/down arrows.  It also enables
**bracketed paste**, so the terminal itself marks where a paste begins and ends
and the pasted block is inserted at the prompt as one piece — a prompt is never
wedged between the pasted lines, and newlines inside a paste do not submit
anything early.  The paste is **submitted only when Enter is pressed**, so a
pasted program can be reviewed (or extended) first.  Clipboard line endings
(CR, CRLF or LF) are all normalised, so pasted lines are echoed on separate
lines rather than overwriting one another:

```text
>>> (define (f x)
  (* x x))
(f 12)
144
>>> 
```

Ctrl-C abandons the line being edited; while a form is running it aborts that
form and returns to the prompt.  A mistyped expression that blocks forever —
say a `chan-recv!` nobody will ever satisfy — therefore waits instead of taking
the session down, and a Go panic inside an evaluation is reported as a single
line rather than a stack dump.

When stdin is not a terminal (a pipe or a redirected file) there is no banner,
no prompt and no line editing, so `echo '(+ 1 2)' | goscheme` simply prints
`3`.  On platforms where raw mode is unavailable the REPL falls back to a
line-oriented reader.

`(command-line)` is the **script name followed by the user's arguments** — the
interpreter's own name never appears, so `(cdr (command-line))` is the argument
list whether the script is interpreted or has been bound into an executable by
`goscheme build`:

```sh
$ goscheme a.scm 1 2 3     ; (command-line) => ("a.scm" "1" "2" "3")
$ goscheme build a.scm
$ ./a.out 1 2 3            ; (command-line) => ("./a.out" "1" "2" "3")
```

(That is a deliberate departure from R7RS, which puts the command name first;
it is what makes one script work unchanged in both forms.)

Exit status: `0` on success, the argument of `(exit n)`, `1` for `(exit #f)` or
for an uncaught error.

## Standalone executables

`goscheme build` turns a script into a single self-contained executable by
**binding an interpreter to it**: it copies the interpreter and appends the
script, so the result needs neither Go nor goscheme on the machine that runs
it.

```sh
$ goscheme build hello.scm        # writes ./a.out, as a C compiler would
$ ./a.out world
hello from a bundled program
argv: ("./hello" "world")
```

The result is an ordinary interpreter image with a trailer:

```
[ interpreter ][ script ][ script name ][ magic ][ trailer length ]
```

Appending to an ELF, PE or Mach-O image is harmless — the loader reads the
headers it knows and ignores the tail — so the file still runs the interpreter,
which at startup checks its own tail and, finding a script there, runs that
instead of the command line.  Nothing is recompiled and the script is stored
verbatim, so a bundle is exactly `interpreter + script + trailer` bytes.

* `-o, --output FILE` names the executable.  Like a C compiler, the default is
  `a.out` in the current directory — or `a.exe` when the bound interpreter is a
  Windows binary.
* `-i, --interpreter FILE` binds a different interpreter, which is how one
  machine can produce executables for another:
  `-i dist/goscheme-windows-amd64.exe` writes a `.exe`.
* `-static` bakes the libraries in.  Every library the script imports, and
  everything those libraries `include`, is resolved at **build** time and
  written in front of the script, so the executable needs no library files
  beside it — and a library that cannot be found is a build error rather than a
  surprise on the user's machine.  Without it, a bundle looks for its libraries
  next to the executable (see [Loading libraries from files](#loading-libraries-from-files)).
* A bundled program's `(command-line)` is `(program arg ...)`, the program as
  it was invoked — see above — and `include` / `load` resolve relative to the
  executable, so data files can be shipped beside it.
* On macOS the appended data invalidates the code signature the linker
  produced, so `goscheme build` re-signs the result ad hoc
  (`codesign --force --sign -`) when it can, and warns when it cannot: Apple
  silicon refuses to run a modified, unsigned binary.

## Repository layout

```
cmd/goscheme/             command line driver
  VERSION                 the version reported by -v and the REPL banner
  version.go              embeds VERSION so any build reports it
  main.go                 file execution, -e, the REPL loop
  bundle.go               goscheme build: binding a script to an interpreter
  static.go               goscheme build -static: resolving libraries up front
  ffi_cgo.go / ffi_stub.go  load-shared-library and foreign-function
  lineedit.go             raw mode line editor and bracketed paste
  term_linux.go           termios raw mode (Linux)
  term_darwin.go          termios raw mode (macOS)
  term_other.go           fallback for platforms without raw mode
  main_test.go            REPL and editor regression tests
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
  b_concurrent.go         channels, (go ...), (select ...) (extension)
  b_process.go            system and system* (extension)
  scheme_test.go          Go unit tests and suite drivers
test/scheme/              Scheme level tests
  r7rs-tests.scm          the reference R7RS test suite
  goscheme-tests.scm      regression tests specific to this implementation
  goscheme-concurrency-tests.scm
                          channels, threads and select
  chibi/test.scm          (chibi test) compatibility shim used by the suites
  run-r7rs.scm            drivers: goscheme run-r7rs.scm
  run-goscheme.scm
  run-concurrency.scm
examples/                 runnable examples, e.g. examples/concurrency.scm
dist/                     `make dist` output: the release binaries, which are
                          attached to GitHub Releases and not tracked by git
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

plus the extension libraries `(goscheme hash-table)`, `(goscheme channel)`
and `(goscheme process)`.

### Loading libraries from files

A library that is not built in is **loaded from the search path**, so a program
can import a library that lives in its own files.  The name `(lib greet)` maps
to `lib/greet.sld` (`.scm`, `.sls` and `.ss` are accepted too), searched in

1. the load path, innermost first — the directory of the script being run, and
   of every file reached through `load` or `include`;
2. the directories in `GOSCHEME_LIBRARY_PATH`, separated by the platform's path
   separator;
3. the working directory.

```scheme
;; lib/greet.sld
(define-library (lib greet)
  (export greet)
  (import (scheme base))
  (begin (define (greet who) (string-append "hello " who))))
```

```scheme
;; main.scm
(import (scheme base) (scheme write) (lib greet))
(display (greet "world")) (newline)
```

Libraries are loaded as they are imported, **recursively**: a library that
imports another one pulls it in, and `include` inside a library resolves
relative to the file that defines it.  A cycle between libraries is reported as
an error rather than recursing forever, and a file that does not define the
library an import asked for is an error too.  An error raised while a library
loads is an ordinary condition, so `guard` around the `import` catches it.

`cond-expand`'s `(library ...)` requirement asks whether a library is
registered *or* findable on the search path.  A program built with
`goscheme build` searches next to the executable, so libraries can be shipped
beside it.

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

### Concurrency (Go flavour)

The interpreter exposes Go's concurrency model to Scheme, in
[`(goscheme channel)`](#libraries):

| Form | Meaning |
|---|---|
| `(make-channel)` | unbuffered channel (a rendezvous) |
| `(make-channel n)` | buffered channel holding up to *n* values |
| `(chan-send! ch v)` | send, blocking until a receiver (or buffer space) is available |
| `(chan-recv! ch)` | receive; returns two values: the value and an *ok?* flag (`#f` once closed) |
| `(chan-close! ch)` | close; closing twice is a no-op |
| `(channel? obj)` / `(channel-open? ch)` | predicates |
| `(go body ...)` | run *body* on a new interpreter thread (a goroutine) |
| `(go-wait)` | wait for every thread started so far |
| `(select ...)` | race several operations, like Go's `select` |

```scheme
(define ch (make-channel))
(go (chan-send! ch 'hello))
(display (chan-recv! ch))            ; prints hello

(select
  (chan-recv! ch1)   => (lambda (v) (display "got: ") (display v))
  (chan-send! ch2 42) => (lambda () (display "sent"))
  (after 1000)       => (lambda () (display "timeout"))
  (else)             => (lambda () (display "idle")))
```

`select` clauses are a flat sequence of `operation => handler` triples; the
handler of a receive clause is called with the received value, the others with
no arguments.  The channel expressions, send values and handlers are all
evaluated before the race begins, and the clauses are checked in the order
written for a ready operation, with `(else)` chosen only when nothing else is
ready — exactly Go's semantics.

Because these are Go's primitives rather than an emulation, Go's rules apply:

* An **unbuffered** channel is a rendezvous; a **buffered** one lets the sender
  run ahead.  A thread can therefore consume its own buffered message, so
  hand-off protocols want an unbuffered channel.
* **`(else)` never waits.**  It turns `select` into a non-blocking poll, so a
  clause that is merely *about* to become ready will be missed.
* **`(go-wait)` waits for every thread started so far**, including long-lived
  server loops that never return — those must be left until last.
* **A script in which every thread blocks** is reported by the Go runtime as
  `all goroutines are asleep - deadlock!` and aborts, as a Go program would.
  The interactive REPL is protected instead: a form that blocks forever simply
  waits, and **Ctrl-C abandons it** and returns to the prompt.
* Errors raised inside a thread are handled by that thread's handlers; an
  uncaught error is printed on the current error port and only ends that
  thread.
* Continuations belong to the thread that captured them; resuming one from
  another thread raises an error.

The interpreter's own shared state is synchronised, so threads may freely share
the global environment, ports and parameters.  Ordinary Scheme data (pairs,
strings, vectors, records) is *not* synchronised — share memory by
communicating.

### Extensions

Beyond R7RS-small the interpreter also provides:

* `(goscheme hash-table)` — `make-eq-hashtable`, `make-eqv-hashtable`,
  `make-equal-hashtable`, `hash-table-ref`, `hash-table-ref/default`,
  `hash-table-set!`, `hash-table-update!`, `hash-table-delete!`,
  `hash-table-exists?`, `hash-table-keys`, `hash-table-values`,
  `hash-table-walk`, `hash-table->alist`, `alist->hash-table`,
  `hash-table-copy`, `hash-table-clear!`, `hash-table-size`, `hash-table-count`
  and `hash`.
* `(goscheme channel)` — `make-channel`, `chan-send!`, `chan-recv!`,
  `chan-close!`, `channel?`, `channel-open?`, `go`, `select` and `go-wait`
  (see [Concurrency](#concurrency-go-flavour) above).
* `(goscheme process)` — `(system command)` runs a command line through the
  system's command processor (`/bin/sh -c`, or `cmd /c` on Windows) and
  `(system* program arg ...)` runs a program directly, both returning the exit
  status as an exact integer: the exit code, or `128+signal` when the child was
  killed by a signal, as a shell reports it.  A program that cannot be started
  raises a file error.  The child inherits the interpreter's standard streams,
  and on a terminal the REPL hands the terminal back to it, so an editor or a
  shell started this way gets a normal cooked terminal.

  ```scheme
  (system "make -j4")                 ; => 0
  (system* "git" "status" "--short")
  (guard (e ((file-error? e) (display "no such program")))
    (system* "/nonexistent"))
  ```
* `(goscheme ffi)` — load a shared library and call a C function in it:

  ```scheme
  (define libm (load-shared-library "libm.so.6"))
  (define cbrt (foreign-function libm 'cbrt 'double 'double))
  (cbrt 27.0)                                    ; => 3.0000000000000004

  (define strlen (foreign-function (load-shared-library #f) 'strlen 'long 'string))
  (strlen "hello")                               ; => 5
  ```

  `(load-shared-library name)` opens a library; `#f` means the running program's
  own symbols, which is where libc lives.  `(foreign-function lib name
  return-type arg-type ...)` returns a procedure that calls the symbol.  The
  types are `void`, `int`/`long`, `double`, `string` (a C `char *`) and
  `pointer`; arguments must be either all integral or all double, with up to
  four integral or three double arguments (every return type works with
  either).  A missing library or symbol, or a mistaken argument type, is an
  ordinary condition.

  A `string` result is copied into a Scheme string; a `pointer` result is the
  address as an exact integer, and a null pointer is `#f`.  String arguments
  are copied for the duration of the call only, so a pointer that a function
  returns *into one of its arguments* (as `strchr` does) is dangling by the time
  you hold it — pass such a pointer straight back to C only if C itself owns
  the memory, as with the pointer `getenv` returns.

  This needs **cgo**: a static Go binary cannot call into a shared library, and
  the released binaries are `CGO_ENABLED=0` so that they stay static and
  cross-compilable.  Those builds still provide the names and raise a clear
  error telling you to rebuild with `CGO_ENABLED=1`; `(features)` reports `ffi`
  when it is available.
* `(assert expr)`, `#!unspecified`, shebang lines (`#!/usr/bin/env goscheme`),
  and the alternative exponent markers `s f d l` accepted by the reader.

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
==   39 passed, 0 failed     concurrency suite (test/scheme/goscheme-concurrency-tests.scm)
```

The concurrency suite also passes under the Go race detector
(`go test -race ./...`).

* `r7rs-tests.scm` is the suite maintained by chibi-scheme. It exercises
  sections 4.1–4.3 (primitive, derived and macro syntax) and 6.1–6.14 (all
  standard procedures), including hygiene corner cases, the numeric grammar,
  reader syntax and cyclic-output handling. It is run through the bundled
  `(chibi test)` shim.
* `goscheme-concurrency-tests.scm` covers channels (buffering, rendezvous,
  closing, `chan-recv!`'s two values), `(go ...)`/`go-wait`, worker pools,
  error handling inside threads and every `select` clause.
* `goscheme-tests.scm` adds regression coverage for proper tail calls,
  multi-shot continuations, `dynamic-wind` re-entry and unwinding, library
  import transformations, records, textual/binary ports, file round-trips,
  `include` / `cond-expand`, hash tables, exceptions and `eval` / `load`.
* The Go tests in `internal/scheme/scheme_test.go` drive both suites and also
  contain direct unit tests for the reader, the numeric tower, tail-call
  behaviour and error propagation.
* `cmd/goscheme/main_test.go` covers the REPL and the line editor: a bracketed
  paste becomes one input block, an end marker split across reads is handled,
  the editing keys and history behave, a piped session prints neither banner
  nor prompts, and output that does not end with a newline is not erased by the
  next prompt.

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
* The concurrency extension follows Go rather than the R7RS/R6RS thread
  proposals: there are no mutexes, condition variables or thread-local dynamic
  state, and `(go-wait)` is a blunt "wait for everything".

## License

MIT — see [LICENSE](LICENSE).  Every source file carries an
`SPDX-License-Identifier: MIT` tag.

Copyright (c) 2026 MrXie1109.

The R7RS reference test suite vendored as `test/scheme/r7rs-tests.scm` is not
part of GoScheme: it comes from [chibi-scheme](https://github.com/ashinn/chibi-scheme)
and stays under its own BSD-3-Clause license, as its header notes.
