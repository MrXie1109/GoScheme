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
- [Embedding in a Go program](#embedding-in-a-go-program)
- [Examples](#examples)
- [Command line](#command-line)
- [Standalone executables](#standalone-executables)
- [Repository layout](#repository-layout)
- [Language coverage](#language-coverage)
- [Extension and SRFI reference](docs/extensions/README.md)
- [Implementation notes](#implementation-notes)
- [The bytecode VM](docs/bytecode.md)
- [Performance](#performance)
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

./.build/goscheme -v   # GoScheme 2.2.0 (R7RS)
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

## Embedding in a Go program

The repository's root package is the interpreter as a library, so a Go program
can use Scheme for configuration or as a plugin language:

```go
import goscheme "github.com/MrXie1109/GoScheme"

i := goscheme.New()
i.Define("double", 1, 1, func(args []goscheme.Value) (goscheme.Value, error) {
    n, ok := args[0].Int()
    if !ok {
        return goscheme.Value{}, fmt.Errorf("double: expected an integer")
    }
    return goscheme.Int(n * 2), nil
})

v, _ := i.Eval("(double 21)")          // 42
square, _ := i.Lookup("square")
v, _ = i.Call(square, goscheme.Int(12)) // Go driving a Scheme procedure
```

`Eval`, `EvalFile`, `Define`, `Lookup`, `Call`, `SetOutput` and `SetArgs` are
the whole surface; `Value` has typed accessors (`Int`, `Float`, `Str`, `Bool`,
`Slice`, `IsNil`, `IsFalse`, `IsProcedure`) so reading a result back does not
need a type switch.  An error returned by a host function becomes an ordinary
Scheme condition, which the Scheme side may catch with `guard`.  Each `Interp`
has its own environment, so two interpreters cannot see each other's
definitions.  `examples/embed/main.go` is a program that uses all of it.

## Examples

`examples/` is a tour of the dialect that runs: the numeric tower, proper tail
calls, hash tables, Go flavoured concurrency, running other programs, what a
script sees, and libraries loaded from files.

```sh
./examples/run-all.sh                    # run them all, report the failures
./.build/goscheme examples/numbers.scm   # or one at a time
```

Every file is commented and prints what it is doing, so they are meant to be read
as much as run; `examples/README.md` says what each one shows.  A Go test runs
them all, so they cannot quietly rot.  Two are worth calling out:

* `examples/libraries/main.scm` imports `(lib greet)` from `lib/greet.sld`, and
  `goscheme build -static` turns it into one executable that needs no library
  files at all;
* `examples/script-args.scm` shows the shape of `(command-line)`, why the
  interpreter's own name is not in it, and how `(assert ...)` and
  `#!unspecified` behave.

## Command line

```
goscheme [options] [file] [argument ...]
goscheme build <script> [-o <output>] [-i <interpreter>]
goscheme compile <script> [-o <output.scmc>]

  -e, --eval EXPR     evaluate EXPR (may be repeated, evaluated in order)
  -i, --interactive   enter the REPL after loading FILE
  -q, --quiet         do not print the REPL banner
  -interp             run in the tree-walker instead of the bytecode VM
  -v, --version       print the version and exit
  -h, --help          print usage
  --                  end of options; the next argument is the script
```

A script is **compiled to bytecode and run on the VM** where the compiler
understands it, and interpreted where it does not; `-interp` forces the
tree-walker, which is how the two are compared.  `goscheme compile` writes the
compiled program to a `.scmc` file, and a `.scmc` file given to the interpreter
is loaded and run without being parsed as source.  See
[docs/bytecode.md](docs/bytecode.md).

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
script, compiled, so the result needs neither Go nor goscheme on the machine
that runs it and starts without reading source.

```sh
$ goscheme build hello.scm        # writes ./a.out, as a C compiler would
$ ./a.out world
hello from a bundled program
argv: ("./hello" "world")
```

The result is an ordinary interpreter image with a trailer:

```
[ interpreter ][ payload ][ script name ][ magic ][ trailer length ]
```

Appending to an ELF, PE or Mach-O image is harmless — the loader reads the
headers it knows and ignores the tail — so the file still runs the interpreter,
which at startup checks its own tail and, finding a program there, runs that
instead of the command line.  Nothing is recompiled on the user's machine, and
a bundle is exactly `interpreter + payload + trailer` bytes.

The payload is the script's **bytecode**, written at build time; a script the
build machine cannot compile — because it imports a library that is only there
when the program runs — is stored as source instead, and `goscheme build` says
so.  Either way the parts the compiler declines run on the tree-walker, exactly
as they do from a file.

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
  bundle.go               goscheme build: binding a compiled script to an interpreter
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
  machine.go              CEK machine, continuations, dynamic-wind, exceptions
  vm.go                   the bytecode VM: instructions, frames, cells
  compile.go              the bytecode compiler, and what it declines
  bytecode.go             the .scmc file format: reading and writing
  b_system.go             files, process context, time, eval and load
  b_hashtable.go          hash tables (extension)
  b_concurrent.go         channels, (go ...), (select ...) (extension)
  b_sync.go               mutexes, wait groups, once, atomics (extension)
  b_socket.go             TCP listeners and connections (extension)
  b_http.go               HTTP client and server (extension)
  b_json.go               JSON (extension)
  b_regexp.go             regular expressions (extension)
  b_time.go               sleeping, clocks, time formatting (extension)
  b_fs.go                 globbing, directory walking, paths (extension)
  b_process.go            system, system* and process pipes (extension)
  b_match.go              the match special form (extension)
  b_fast*.go              the (goscheme fast) library (extension)
  ffi_cgo.go              load-shared-library and foreign-function (extension)
  scheme_test.go          Go unit tests and suite drivers
  docs_test.go            checks that docs/extensions covers every export
test/scheme/              Scheme level tests
  r7rs-tests.scm          the reference R7RS test suite
  goscheme-tests.scm      regression tests specific to this implementation
  goscheme-concurrency-tests.scm
                          channels, threads and select
  chibi/test.scm          (chibi test) compatibility shim used by the suites
  run-r7rs.scm            drivers: goscheme run-r7rs.scm
  run-goscheme.scm
  run-concurrency.scm
examples/                 runnable examples and their runner (see examples/README.md)
dist/                     `make dist` output: the release binaries, which are
                          attached to GitHub Releases and not tracked by git
scripts/build-dist.sh     cross-compilation script used by `make dist`
docs/extensions/          one reference page per (goscheme ...) library
docs/srfi/                one reference page per (srfi N) library
docs/ffi-design.md        the design notes behind (goscheme ffi)
docs/development.md       building, testing and cross-compiling locally
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

String escapes are the report's — `\a \b \t \n \r \f \v \" \\ \|`,
`\xHH;` with any number of hex digits, and backslash-newline continuation —
plus three that make terminal code writable: `\e` for ESC, `\xHH` without the
semicolon, and C-style octal `\NNN`, so `"\033[31m"` is what it looks like.

### Libraries

`(scheme base)` `(scheme case-lambda)` `(scheme char)` `(scheme complex)`
`(scheme cxr)` `(scheme eval)` `(scheme file)` `(scheme inexact)`
`(scheme lazy)` `(scheme load)` `(scheme process-context)` `(scheme read)`
`(scheme repl)` `(scheme time)` `(scheme write)` `(scheme r5rs)`

plus the extension libraries `(goscheme hash-table)`, `(goscheme channel)`,
`(goscheme fast)` and `(goscheme process)`.

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

469 bindings are installed in the standard environment, and the 28 builtin
libraries export 676 names — 469 distinct, since the `(scheme …)` libraries
re-export the same ones.  The coverage includes
total. Coverage includes the numeric tower (`exact-integer-sqrt`,
`rationalize`, `floor/`, `truncate/`, `make-polar`, `number->string` with any
radix, …), list and vector operations, Unicode-aware string and character
operations, textual and binary I/O, file and process-context procedures,
`eval` / `load` / `environment`, `dynamic-wind`, `guard`,
`with-exception-handler`, `parameterize`, promises and `values`.

## Implementation notes

### Two execution paths

Expressions the compiler understands run as bytecode on a stack machine, and
everything else runs in the tree-walker; a body that uses a form the compiler
does not handle is interpreted as a whole, so the two paths agree by
construction rather than by imitation.  Macros are expanded before compilation,
tail calls are instructions of their own, and a continuation captured inside
compiled code works because the VM's frames are written once and never mutated,
exactly like the interpreted ones.  [docs/bytecode.md](docs/bytecode.md) has
the instruction set, the `.scmc` file format, the measured numbers and what the
compiler declines.

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
evaluated before the race begins.  When several clauses are ready at once the
winner is chosen **at random**, exactly as Go's `select` does, so the order the
clauses are written in decides the order of evaluation and not which ready
clause wins.  `(else)` is chosen only when nothing else is ready.

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

Every library below has a lookup reference under
[`docs/extensions/`](docs/extensions/README.md) — and the SRFI libraries under
[`docs/srfi/`](docs/srfi/README.md) — with every exported procedure, its
arguments and what it does.  Here is the summary.

* `(goscheme fast)` — 158 procedures for the jobs Scheme does slowly, done in
  Go.  Each one keeps its loop, its indexing, its copying and its sorting
  native instead of taking an interpreted step per element:

  * ordering — `sort`, `sort!`, `sort-by`, `vector-sort`, `vector-sort-by`,
    `vector-binary-search`, `vector-binary-search-insert`
  * sequences — `iota`, `vector-iota`, `range`, `take`, `drop`, `take-right`,
    `drop-right`, `split-at`, `last`, `chunk`, `vector-take`, `vector-drop`,
    `vector-chunk`, `vector-concat`, `vector-reverse`, `vector-reverse!`,
    `vector-swap!`
  * selection — `filter`, `filter-not`, `vector-filter!`, `count`, `any`,
    `every`, `find`, `list-index`, `delete`, `delete-duplicates`, `partition`,
    `vector-partition`, `vector-index-of`, `zip`, `unzip`, `flatten`
  * folding — `fold-left`, `fold-right`, `assoc-set`
  * aggregates — `sum`, `product`, `min-of`, `max-of`, `vector-dot`,
    `vector-norm`, `vector-argmin`, `vector-argmax`, `mean`, `median`,
    `percentile`, `variance`, `stddev`, `mode`
  * batch arithmetic — `vector-add`, `vector-sub`, `vector-mul`, `vector-div`,
    `vector-scale`, `vector-negate`, `vector-abs`, `vector-clamp`,
    `vector-prefix-sum` — the first eight with an in-place `!` variant that
    writes into the caller's vector — plus `vector-equal?` and
    `vector-compare`
  * number theory — `bit-and`, `bit-or`, `bit-xor`, `bit-not`, `bit-shift`,
    `bit-count`, `integer-length`, `expt-mod`, `isqrt`, `prime?`, `primes`,
    `factor`, `clamp`, `sign`
  * strings — `string-split` (with an optional limit), `string-join`,
    `string-contains`, `string-index`, `string-index-from`,
    `string-last-index`, `string-find-all`, `string-count`, `string-prefix?`,
    `string-suffix?`, `string-prefix-ci?`, `string-suffix-ci?`, `string-trim`,
    `string-trim-left`, `string-trim-right`, `string-replace`,
    `string-replace-first`, `string-pad-left`, `string-pad-right`,
    `string-pad-center`, `string-reverse`, `string-repeat`, `string-chunk`,
    `string-sort`, `string-take`, `string-drop`, `string-fields`,
    `string-lines`, `string-titlecase`, `string-upper`, `string-lower`,
    `string-blank?`, `string-empty?`, `string-chomp`, `string-integer?`,
    `string-byte-length`
  * bytes and hashing — `sha256`, `sha1`, `sha512`, `md5`, `hmac-sha256`,
    `crc32`, `random-bytes`, `hex-encode`, `hex-decode`, `base64-encode`,
    `base64-decode`, `base64url-encode`, `base64url-decode`, `base32-encode`,
    `base32-decode`, `bytes-xor`, `bytes-and`, `bytes-or`, `bytes-not`,
    `bytes-reverse`, `bytes-index`, `bytevector-fill!`, `vector->bytevector`,
    `bytevector->vector`
  * randomness — `random-int`, `random-float`, `random-string`,
    `random-choice`, `shuffle`, `vector-shuffle!`, `vector-sample`, `uuid`

  The names are R7RS-shaped where an R7RS name exists — `sort` takes an
  optional `less?`, `string-split` an optional separator — and distinct where
  one does not, so importing `(goscheme fast)` next to `(scheme base)` or
  `(scheme char)` never clashes.  A `less?` or predicate that is a builtin
  (`<`, `string<?`, `even?`, `string?`) is applied without a call back into
  Scheme; a predicate you write yourself still pays one call per element, so
  there the library is convenience rather than speed.  `delete-duplicates` and
  `assoc-set` take an optional comparison the same way, and the `!` procedures
  return the vector they wrote into.  Measured numbers are in
  [Performance](#performance); `examples/fast.scm` times itself and then tours
  the library.

* `(goscheme sync)` — the other half of the concurrency story: `make-mutex`,
  `mutex-lock!`, `mutex-unlock!`, `with-mutex` (which releases the lock however
  the body leaves), `make-waitgroup` with `waitgroup-add!`/`-done!`/`-wait`,
  `make-once` with `once-run!`, lock-free `make-atomic` counters, and the
  matching `mutex?`, `waitgroup?`, `once?` and `atomic?` predicates.  Beware
  that unlocking a mutex the thread does not hold is a Go runtime fatal error
  rather than a condition (see [Known limitations](#known-limitations)).
* `(goscheme socket)` — `(tcp-listen port [host])`, `(tcp-accept listener
  [mode])`, `(tcp-connect host port [mode])`, `tcp-listener-port`,
  `tcp-listener-address`, `tcp-listener?`, `tcp-address`,
  `tcp-close-listener`.  A connection is an ordinary port in both directions, so
  `read-line` and `write-string` are the whole protocol vocabulary, and `(go
  ...)` turns a listener into a server.  The host defaults to the loopback
  address; `mode` is `'textual` or `'binary`.
* `(goscheme http)` — `http-get`, `http-post`, `http-put`, `http-delete`,
  `http-head` return the body; `http-request` returns the whole response
  (`http-response-status`, `-body`, `-header`, `-content-type`).  `(http-serve
  port handler [host])` starts a server whose handler is an ordinary procedure
  taking a request (`http-request-method`, `-path`, `-query`, `-header`,
  `-body`); net/http runs one interpreter thread per request, and a handler that
  raises becomes a 500 rather than a dead process.  The methods that can carry
  a body take `(url [body [headers]])`, where a string in the second position is
  the body and an alist is the headers; a request body handed to a handler is
  read up to 8 MiB and no further.
* `(goscheme json)` — `json-parse` and `json-write`.  Objects are `equal?` hash
  tables with string keys, arrays are vectors, and JSON `null` is the symbol
  `null`; integral literals stay exact however many digits they have.  On the
  way out `json-write` takes those same hash tables as objects — an alist is not
  accepted — and emits the keys in sorted order, so a round trip does not
  preserve insertion order.
* `(goscheme regexp)` — `regexp`, `regexp-match` (with capture groups),
  `regexp-match?`, `regexp-match-positions`, `regexp-replace` (with `$1`),
  `regexp-replace-all`, `regexp-split`, using Go's RE2 engine.  Every one of
  them also accepts a bare pattern string, which it compiles for that call, and
  the positions `regexp-match-positions` returns are byte offsets rather than
  character indices.
* `(goscheme time)` — `sleep`, `current-millisecond`, `monotonic-millisecond`,
  `time-format`, `time-parse`, `time-utc-parts`.  Durations are milliseconds,
  the same unit `(after ms)` takes, and the layout `time-format` and
  `time-parse` expect is Go's reference layout (`2006-01-02 15:04:05`), not
  `strftime`.  `time-utc-parts` is a seven-pair association list of
  `(year month day hour minute second weekday)`.
* `(goscheme fs)` — `glob`, `directory-walk`, `directory-list`,
  `create-directory`, `create-directory-tree`, `delete-directory`,
  `delete-directory-tree`, `path-join`, `path-directory`, `path-base`,
  `path-extension`, `path-absolute`, `file-size`.
* `(goscheme match)` — pattern matching: `_`, symbols (which bind), `(quote
  datum)`, literals, `()`, `(p ... . rest)`, `#(p ...)`, and `(and ...)`,
  `(or ...)`, `(not ...)`.  A clause is `(pattern body ...)` or `(pattern
  (guard test) body ...)` — the guard follows the pattern, it is not inside it:
  `((n (guard #t)) body)` is a pattern matching a two-element list.  `else`
  works as a second spelling of `_`, and a name repeated inside one pattern
  silently keeps the last binding instead of requiring the two to be equal.
* `(goscheme process)` — the `popen` pair, built directly on `os/exec` rather
  than on `system`: `(open-input-process program arg ...)` is a port on the
  child's output, `(open-output-process program arg ...)` a port on its input,
  and `(process-status port)` its exit status once the port is closed (before
  that, `#f`).  Two of them joined by a loop are a pipeline.  A program that
  cannot be started raises a file error from `system*` and the `open-*-process`
  forms, while `system` goes through the shell and so reports a missing command
  as the shell's own exit status 127.

Beyond R7RS-small the interpreter also provides:

* `(goscheme hash-table)` — `make-eq-hashtable`, `make-eqv-hashtable`,
  `make-equal-hashtable`, `make-hash-table`, `hash-table-ref`,
  `hash-table-ref/default`, `hash-table-set!`, `hash-table-update!`,
  `hash-table-delete!`, `hash-table-exists?`, `hash-table-contains?`,
  `hash-table-keys`, `hash-table-values`, `hash-table-walk`,
  `hash-table->alist`, `alist->hash-table`, `hash-table-copy`,
  `hash-table-clear!`, `hash-table-size`, `hash-table-count`, `hash`, and the
  `hash-table?` / `hashtable?` predicates.  `make-hash-table` also takes a size
  hint and an equivalence — a symbol or procedure naming `eq?`, `eqv?` or
  `equal?` — `alist->hash-table` fills the table you hand it rather than always
  building a new one, and the optional fourth argument of `hash-table-update!`
  is the value to use when the key is missing, not a thunk.
* `(srfi 1)` — the list library: `fold`, `unfold`, `reduce`, `take-while`,
  `span`, `delete-duplicates`, the `lset-` set operations and the rest of the
  SRFI-1 API.  The sixteen names it shares with `(goscheme fast)` — `filter`,
  `take`, `drop`, `iota`, `zip`, `any`, `every` and friends — are the *same
  binding* exported from both libraries, so importing both cannot make them
  disagree.
* `(srfi 2)`, `(srfi 8)`, `(srfi 26)` and `(srfi 111)` — `and-let*`,
  `receive`, `cut`/`cute` and boxes (`box`, `unbox`, `set-box!`, `box?`).  The
  three macros are written in Scheme and embedded in the binary, so they
  survive `goscheme build`.
* `(srfi 128)` — comparators: `make-comparator`, the `=?`, `<?`, `>?`, `<=?`
  and `>=?` chains, `comparator-if<=>`, the ready-made `eq?`/`eqv?`/`equal?`
  comparators, `make-default-comparator`, and the hash functions including the
  case-insensitive ones, with `hash-bound` and `hash-salt` as parameters.
* `(srfi 133)` — the vector library: `vector-unfold`, `vector-fold`,
  `vector-map!`, `vector-count`, `vector-index`, `vector-skip`,
  `vector-any`/`vector-every`, `vector-partition`, `subvector`,
  `vector-concatenate`, `vector-append-subvectors` and the rest.  The names
  R7RS and `(goscheme fast)` already define (`vector-copy`, `vector-map`,
  `vector-swap!`, `vector-binary-search`, ...) are the *same bindings*, so
  importing all three cannot make them disagree.
* Each has a reference page under [`docs/srfi/`](docs/srfi/README.md), and
  [`docs/manual/`](docs/manual/README.md) is the tutorial that puts them
  together.

* `(goscheme channel)` — `make-channel`, `chan-send!`, `chan-recv!`,
  `chan-close!`, `chan-open?`, `channel?`, `nil-channel`, `nil-channel?`,
  `go`, `select` and `go-wait` (see [Concurrency](#concurrency-go-flavour)
  above).  `nil-channel` is Go's nil channel: never ready, so
  `(set! ch (nil-channel))` takes a clause out of a `select` for good, which
  closing it would not — a closed channel is always ready.
* `(continue)` — inside a `do` body, abandons the rest of the body and goes on
  with the loop's step expressions, which still run: Go's `continue`.  Unlike
  Go's it is dynamic rather than lexical, so a procedure the body calls may use
  it; it is implemented as a raise of a private condition, so a `guard` in the
  body that catches everything can intercept it.  Using it outside a loop is an
  ordinary, catchable condition.
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
  four integral or three double arguments; the declared return type has to
  match what the C function actually returns, because a mismatch reinterprets
  the returned register rather than converting it, and a `void` result is an
  unspecified value.  A missing library or symbol, or a mistaken argument type, is an
  ordinary condition.

  Opening a library is the one part that differs by platform: the POSIX hosts use
  `dlopen`, and Windows uses `LoadLibrary`/`GetProcAddress`, where `#f` means the
  running executable.  On Windows the C library is not part of the program's own
  exports, so name one explicitly — `ucrtbase.dll` for `strlen`, `cbrt` and the
  rest, or `kernel32.dll` for the Windows API.

  A `string` result is copied into a Scheme string; a `pointer` result is the
  address as an exact integer, and a null pointer is `#f`.  String arguments
  are copied for the duration of the call only, so a pointer that a function
  returns *into one of its arguments* (as `strchr` does) is dangling by the time
  you hold it — pass such a pointer straight back to C only if C itself owns
  the memory, as with the pointer `getenv` returns.

  This needs **cgo**, so it is available in the `-dynamic` builds and in any
  build you make with `CGO_ENABLED=1`; `(features)` reports `ffi` when it is
  there.  The static builds still provide the names, and calling one raises a
  clear error telling you which binary to use instead.

  Loading a shared library is the dynamic loader's job, and a statically linked
  program has no loader to do it with: on ELF platforms FFI and static linking
  cannot both be had, whichever toolchain is used.  `docs/ffi-design.md` records
  the measurements behind that (including what musl and glibc each do) and what
  would have to change upstream for it to become possible.
* `(assert expr)`, `#!unspecified`, shebang lines (`#!/usr/bin/env goscheme`),
  and the alternative exponent markers `s f d l` accepted by the reader.

## Performance

It is a tree-walking interpreter over an explicit continuation stack, so it is
not the fastest Scheme there is; the numbers below are what the design costs,
and `go test ./internal/scheme -bench BenchmarkPrograms` reproduces them (they
were measured on a 12th-generation i3).

| Program | Time |
|---|---|
| `(fib 22)`, 28k calls | ~51 ms |
| 500,000 turns of a tail loop | ~0.58 s |
| 200,000 additions and comparisons | ~0.31 s |
| building a 20,000 element list | ~34 ms |
| 200,000 closure creations and calls | ~260 ms |

Three things get the most out of the machine: an application no longer copies
its operands into a slice (the frames walk the argument list and keep the values
in a small array), small exact integers are cached and compared without going
through `big.Rat`, and an environment frame holds its first four bindings in its
own fields instead of allocating a map.  Together they are about twice the speed
of 2.1.1.

`(goscheme fast)` is where the slow jobs were moved to Go.  Measured with `go
test ./internal/scheme -bench BenchmarkFast`, each pair building its input the
same way so that only the operation under test differs:

| Job | In Scheme | `(goscheme fast)` |
|---|---|---|
| merge sort 1,500 numbers | ~58 ms | ~0.9 ms |
| filter 20,000 numbers with `even?` | ~40 ms | ~2.3 ms |
| build a 20,000 element list | ~32 ms | ~1.7 ms |
| find `"xxxy"` in 20,000 characters | ~56 ms | ~1.4 ms |
| add two 20,000 element vectors | ~61 ms | ~3.3 ms |
| deduplicate 4,000 elements | ~62 ms | ~4.7 ms |
| sieve the primes below 30,000 | ~1.26 s | ~0.9 ms |

The vector procedures are the batch lane: one Go pass over the whole vector,
with an in-place variant that allocates nothing, instead of one interpreted
step per element.  There is no SIMD behind them; what disappears is the
per-element type check, the boxing of intermediate results and the interpreted
step itself.

A predicate written in Scheme is the exception: `filter` has to call it once
per element, which costs about what the Scheme loop cost, so the library's
predicates are worth using only when a builtin one will do.

**The bytecode VM** is a bigger win than that, and it is the default execution
path.  On a panel of twelve programs — calls and arithmetic, closures, global-
and local-heavy loops, lists, vectors, strings, higher-order code, a small
evaluator, a merge sort, tail loops and `call/cc` re-entry — it is **1.33× to
4.99× faster** than the tree-walker (geometric mean 3.0×) and allocates **82%
less**.  Bindings are resolved at compile time; macros are expanded once rather
than on every evaluation; a call to a builtin such as `+` or `car` builds no
continuation frame at all; and a call to a compiled procedure replaces the
current activation instead of recursing, so a non-tail recursion a million deep
costs two Go stack frames.  It also means a program can be compiled once and
stored: `goscheme compile script.scm` writes a `.scmc` file that runs without
being parsed as source.  [docs/bytecode.md](docs/bytecode.md) has the table, the
caveats and what the compiler declines.

A frame takes its lock only while more than one interpreter thread is running,
which is what makes the common single-threaded case free; `(go ...)` and the
HTTP handler raise the count before they start, so shared environments are
still locked exactly as before.

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
==  244 passed, 0 failed     GoScheme regression suite (test/scheme/goscheme-tests.scm)
==   83 passed, 0 failed     concurrency suite (test/scheme/goscheme-concurrency-tests.scm)
==   42 passed, 0 failed     network suite (test/scheme/goscheme-network-tests.scm)
==   35 passed, 0 failed     process and fs suite (test/scheme/goscheme-process-tests.scm)
==   86 passed, 0 failed     data suite (test/scheme/goscheme-data-tests.scm)
==   43 passed, 0 failed     match suite (test/scheme/goscheme-match-tests.scm)
==  462 passed, 0 failed     fast suite (test/scheme/goscheme-fast-tests.scm)
==  175 passed, 0 failed     SRFI-1 suite (test/scheme/srfi-1-tests.scm)
==   47 passed, 0 failed     small SRFI suite (test/scheme/srfi-small-tests.scm)
==   73 passed, 0 failed     SRFI-133 suite (test/scheme/srfi-133-tests.scm)
==   94 passed, 0 failed     SRFI-128 suite (test/scheme/srfi-128-tests.scm)
```
That is 2611 assertions in total, and the count for the goscheme suite is 244
with cgo rather than 229 without it, because the FFI section only runs when the
build has it.

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

`make dist` (or `scripts/build-dist.sh`) produces the six static binaries into
`dist/`, which is what the releases carry.  A *dynamic* flavour — `CGO_ENABLED=1`,
linked against the platform's C library, and the only one with `(goscheme ffi)` —
is built by the same script wherever a C compiler for the target exists
(`DYNAMIC_PLATFORMS`, with `CC_<os>_<arch>` to name the compiler), and the CI
workflow builds it for macOS and Windows; those are not attached to the
releases.

| Artifact | Platform | Flavour |
|---|---|---|
| `goscheme-linux-amd64` | Linux x86-64 | static |
| `goscheme-linux-arm64` | Linux AArch64 | static |
| `goscheme-darwin-amd64` | macOS Intel | static |
| `goscheme-darwin-arm64` | macOS Apple Silicon | static |
| `goscheme-windows-amd64.exe` | Windows x86-64 | static |
| `goscheme-windows-arm64.exe` | Windows on ARM | static |

The **static** binaries are `CGO_ENABLED=0`: no dependencies at run time and no
FFI, so `(features)` does not report `ffi`.  The **dynamic** ones are
`CGO_ENABLED=1`, linked against the platform's C library, and are the only
flavour that can load shared libraries, so they are the ones with
`(goscheme ffi)`; they need that C library at run time, and the Linux binaries
here were built against glibc 2.34, so they need a glibc at least that new.

The darwin dynamic builds and the Windows one are produced by
`.github/workflows/ci.yml` on the platforms themselves, which is also how
macOS is tested at all: a Mach-O binary cannot be executed on the machine this
was developed on, so CI runs the suites natively on macOS, Linux and Windows.

A dynamic build needs a C compiler *for the target*, which is why the default
list names the targets this machine can build: the two Linux ones, and
windows/amd64 with a mingw toolchain.  Name the others in `DYNAMIC_PLATFORMS` if you
have the cross compilers — `CC_<os>_<arch>` overrides the compiler for each
target, and a target whose compiler is missing is skipped with a message rather
than failing the run:

```sh
DYNAMIC_PLATFORMS="linux/amd64" make dist
CC_windows_amd64=x86_64-w64-mingw32-gcc make dist
```

Build flags: `GOOS=<os> GOARCH=<arch> CGO_ENABLED=<0|1> go build -trimpath
-ldflags "-s -w"`.  `dist/SHA256SUMS` records the checksum of every artifact.

## Requirements

* Go 1.22 or newer (the module targets `go 1.22`).
* No third-party modules — the standard library only.
* The interpreter itself runs on Linux, macOS and Windows, on amd64 and arm64.

## Known limitations

* `define-syntax` supports `syntax-rules` transformers only, which is the full
  extent of the R7RS-small macro system.
* The target language is R7RS-small.  R7RS-large as a whole is not provided;
  what is provided beyond R7RS-small is the hash-table extension and the SRFIs
  listed under [Extensions](#extensions) (SRFI-1, 2, 8, 26, 111, 128 and 133 so
  far).
* It is a tree-walking interpreter — there is no compiler or JIT. Tail calls
  are proper, but deep non-tail recursion allocates heap frames.
* Inexact numbers are printed with Go's shortest round-trip representation, and
  symbols that merely look like numbers (for example `+NaN.0abc`) are quoted
  with `|…|` by `write`.
* `write-simple` on cyclic data may not terminate, which the report permits;
  `display` does not keep a visited set either, so it can hang on cyclic data
  where `write` survives it.
* The concurrency extension follows Go rather than the R7RS/R6RS thread
  proposals: there is no thread-local dynamic state and there are no condition
  variables, and `(go-wait)` is a blunt "wait for everything".  `(goscheme
  sync)` does provide the mutexes, wait groups, one-time runs and atomics a Go
  programmer expects.
* An imported binding is a copy. A library that mutates its own variable with
  `set!` and an importer that reads it can therefore diverge, and `set!` on an
  imported name changes only the importer's copy.  chibi-scheme behaves the
  same way and [the R7RS text is ambiguous on the point](http://scheme-reports.org/mail/scheme-reports/msg03546.html),
  but it is worth knowing before relying on it.
* Multiple values in a single-value context are truncated to the first rather
  than reported, and a zero-value result becomes the unspecified value.
* Two misuses abort rather than raise, so `guard` cannot help: `mutex-unlock!`
  on a mutex the calling thread does not hold is a Go runtime fatal error, and
  an `(after ms)` select clause whose `ms` is negative or not an exact integer
  panics while the clause is being evaluated.

## License

MIT — see [LICENSE](LICENSE).  Every source file carries an
`SPDX-License-Identifier: MIT` tag.

Copyright (c) 2026 MrXie1109.

The R7RS reference test suite vendored as `test/scheme/r7rs-tests.scm` is not
part of GoScheme: it comes from [chibi-scheme](https://github.com/ashinn/chibi-scheme)
and stays under its own BSD-3-Clause license, as its header notes.
