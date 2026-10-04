# The bytecode VM

GoScheme runs a program two ways, and they are meant to be indistinguishable:

* **compiled** — a body the compiler understands becomes bytecode and runs on
  the stack machine in `internal/scheme/vm.go`;
* **interpreted** — the tree-walker in `internal/scheme/machine.go`, which is
  what everything ran on before this existed.

This page is the overview; [bytecode-internals.md](bytecode-internals.md) is the
reference underneath it — the file format, the instruction set, the call
protocol in full, and the measurements.

The compiler takes every form the language has.  Where a form means another
form it calls the interpreter's own expansion and compiles the result; where it
needs something at run time, it compiles the pieces into procedures and hands
them to a helper.  The two paths therefore agree by construction, and the tests
are there to prove the construction right — see [Guarantees](#guarantees)
below.  This was not always so: 3.0.0 declined thirteen forms, and 3.1.0 took
them all.

```sh
goscheme script.scm                 # compiled where possible (the default)
goscheme -interp script.scm         # the tree-walker, for comparison
goscheme compile script.scm -o script.scmc
goscheme script.scmc                # run bytecode without reading source
```

## How it runs

`internal/scheme/compile.go` compiles one **body** at a time: a top-level form,
or a lambda body.  Inside a body it handles

* `quote`, `quasiquote` (expanded into the constructors the interpreter's own
  expander produces), `if`, `begin`, `lambda`, `set!`, `define` (top-level and
  internal),
* `let`, `let*`, `letrec`, `letrec*`, named `let`,
* `and`, `or`, `when`, `unless`, `cond`, `case` (including `=>`),
* `guard`, whose clauses compile to a procedure of the condition and whose body
  to a thunk, both handed to a runtime helper that installs the handler the
  interpreter installs,
* the concurrency forms `go` and `select`, whose bodies and clause expressions
  compile to thunks that a helper spawns or chooses between,
* `cond-expand`, which chooses its clause while compiling, and
* `define-syntax`, `let-syntax` and `letrec-syntax` in a body, whose macros the
  compiler expands where the interpreter would have put them in an environment,
  and
* applications, tail calls, and every self-evaluating literal.

plus anything a macro expands into, because **macros are expanded at compile
time** with the same expander the interpreter uses.

That is the whole language: **nothing is left for the interpreter to decide.**
It was not always so.  `do`, `guard`, `parameterize`, `define-record-type`,
`define-values`, `let-values`, `case-lambda`, `delay`, `quasiquote`, `match`,
`select` and `go` each used to make the compiler decline a whole body.  Each is
now a form the compiler takes, most of them by expanding into forms it already
took — a `do` loop is a `letrec` whose body is guarded, a `let-values` is
`call-with-values` around fresh temporaries, `quasiquote` is `cons` and
`append` — or by handing a runtime helper the pieces it needs as compiled
procedures, which is how `guard`, `select`, `match`, `go` and the record types
work.  The matching in `match`, the selection in `select` and the handler in
`guard` are the interpreter's own code: only the pieces around them became
bytecode, so the two paths cannot disagree about what a pattern matches or
which clause wins.

A macro defined in a body is the one place where compiling needs something the
compiler does not have: the interpreter evaluates a transformer in the
environment where it is written, and a compiled body's variables are slots, not
an environment.  The compiler therefore keeps a **shadow environment** beside
every scope — a real `Env` with the same names and no values — purely so that
the marks a template's identifiers carry name a scope, that scope names a
block, and that block names the slot the variable lives in.  Hygiene comes out
the same as the interpreter's for every macro whose definition scope encloses
its use, which is every macro that does not escape its scope.

The compiler's table is a list of what it *does* handle rather than what it does
not, so adding a form is a local change to `special` in `compile.go`.

A file is compiled when it is **run**, not only by `goscheme compile`.  The
command line, `load`, a library file and an embedded extension all go through
the same two calls, so "compiled where possible" describes the default
execution path rather than something a program has to opt into.  It did not
always: running a script went through `RunForms`, which makes the whole file a
single body, and a body is all or nothing — so one `import` at the top of a
file sent every form in it to the tree-walker.  Since every test suite starts
with an import, the suites were being interpreted in *both* modes, and so was
most of what anyone runs.  The first time the suites were actually compiled
they found three compiler bugs (a `case` `(else => proc)`, a macro that defines
a macro, and `=>` shadowed by a local binding), which is the argument for
compiling the tests rather than only the benchmarks.

### Frames, variables and continuations

* An activation's variables live in a **frame** (`vmEnv`): a slice of slots
  plus the frame the closure was created in.  A variable reference compiles to
  (depth, slot), resolved at compile time.
* A variable that `set!` changes lives in a **cell**, so a closure and the
  frame it came from see the same binding.
* A variable introduced by an internal `define` or by `letrec` is **checked**:
  reading it before its initializer has run is an error, as in the interpreter.
* **Tail calls** are instructions of their own (`opTailCall`), so a loop is a
  loop and not a growing stack.
* **Continuations.**  A compiled activation is *not* on the continuation stack
  while it runs; it puts a frame there only while it waits for a call it made.
  That frame is written once and never mutated — a resumption gives the
  activation a fresh operand stack.  So a continuation captured by `call/cc`
  inside compiled code can be invoked any number of times, and it composes with
  `dynamic-wind` and `guard` because those are the machine's frames, not the
  VM's.

### The call protocol

A call does not nest Go calls, and it does not wait to find out what it is
calling.  `vmRun` is a loop with a *current activation*; `opCall` does one of
three things:

* **a simple primitive** — one registered through `defSimple`, 596 of the 644
  builtins.  Its Go function cannot reach the machine at all, so it can only
  return a value or raise; either way it finishes inside the call, and no
  continuation frame is built.  A `(+ a b)` in a loop is a call with no
  allocation.  (If it raises, the frame the caller *would* have built is built
  at that point, which is indistinguishable: the primitive pushed nothing.)
* **a compiled procedure** — its body becomes the current activation, in the
  same Go frame, after the caller's state is saved.  The caller's operand array
  is handed on as the callee's.
* **anything else** — an interpreted closure, a `call/cc` continuation, a
  parameter, a multi-clause or `case-lambda` procedure, a primitive that calls
  back into Scheme: the frame is pushed and the machine's `apply` runs it.

Because the second case replaces the activation instead of recursing, a
non-tail recursion a million deep costs a million frames on the Scheme stack
and *two* Go ones.  A tail call is the same replacement with nothing saved, so
it costs neither.

## Bytecode files

`goscheme compile` writes a **`.scmc`** file: a shebang line, a magic number, a
version, and the program as chunks.  The interpreter decides how to read a file
by looking at it and not at its name — a compiled file is written executable and
may be installed under a name with no extension at all, which is what a command
is called — so a `.scmc` file renamed to anything still runs, and a source file
named `something.scmc` is still read as source.  A chunk is either compiled code or a source form, and the
source ones are exactly the forms that teach the compiler something
(`import`, `define-syntax`, `include`) or that it could not compile — which is
why loading a `.scmc` file still applies its imports and still defines its
macros, while the rest of the program is never parsed again.

```
#!...  | GSCM 05 | symbol count | symbol ... | chunk count | chunk ... 
chunk   = 0 <datum>            ; a source form to evaluate
        | 1 <code>             ; bytecode to run
        | 2 <count> <chunk>... ; steps that share one continuation extent
code    = name | instructions | constants | frame shape | parameters
datum   = tagged and recursive: numbers (exact, rational, inexact), strings,
          symbols (interned on load), characters, booleans, pairs, vectors,
          bytevectors, and nested code
```

Consecutive ordinary forms become **one chunk holding a `begin`**, because that
is what running the same file does: a continuation captured in one top-level
form has to span the rest of the program, and a chunk boundary would end it.

If that `begin` ever fails to compile as a whole — a body is all or nothing,
which is what the compiler's "I cannot do this form" amounts to — the forms are
compiled one at a time instead and written as the **steps** of a single chunk
(`2` above).  They still run in one extent, so the continuation rule above holds
either way.  Nothing in the language reaches that path any more, so it is the
safety net rather than the common case: it is what a compiler extension or a
mistake falls back to, and it has a test of its own.

The first line is a shebang, `#!/usr/bin/env goscheme`, because the file is
written executable and can be run as it stands:

```sh
goscheme compile prog.scm -o prog.scmc
./prog.scmc
```

It is a line of text rather than part of the format, and the reader skips it
whenever it is there: a file compiled before this existed — with no shebang —
reads exactly as it always did, and so does one whose shebang names something
else.  Only the four bytes after it have to be the magic.

### Obfuscated output

`goscheme compile -obfuscate` writes a file that runs the same and does not read
like a description of the program:

```sh
goscheme compile prog.scm -o prog.scmc -obfuscate
```

* every compiled body is renamed `b0`, `b1`, …;
* slot names become `v0`, `v1`, … — they stay in the file, because
  `opLocalCheck` uses them to say which variable was used before it was
  initialized, so the message survives and the name in it does not;
* each constant pool is shuffled, and the instructions that index it are
  rewritten to match, so which constant sits at index 0 is not a clue about
  where the code begins.  Compiling twice gives different bytes;
* the globals the program defines itself are renamed (`secret-key` → `g0`),
  but only where it can be shown that nothing else refers to them by name: a
  name mentioned in a source chunk keeps its name, because that chunk is
  evaluated by the interpreter as written, and a name the program does not
  define belongs to a library.

It is not encryption, and does not pretend to be: the strings and numbers the
program works with are still in the file, because it needs them, and a reader
can still follow the instructions.  What goes is the vocabulary — the names that
say what the program is *for*.  A program that builds a symbol from a string and
looks it up keeps that name, and is not caught.

The file stays a valid `.scmc` file of the same version: the interpreter reads
an obfuscated program without knowing it was obfuscated, because the names were
never needed to run it.

Version 5 added the symbol table — every name in the file written once, and
referred to by index afterwards, ordered so that the names used most often get
the shortest index.  The R7RS test suite compiles 24.5% smaller because of it
(215630 bytes to 162814).  Version 4 added the comparison instructions, version
3 the primitive reference in a constant pool and version 2 the steps chunk; older files are still read, and a file
from a newer version than the interpreter speaks is refused rather than
misread.

## Guarantees

The two paths are held together by tests, not by hope:

* `internal/scheme/vm_test.go` runs a corpus of 65 programs — arithmetic,
  closures and `set!`, internal definitions, every compiled derived form, tail
  calls, a 200 000-deep non-tail recursion, `call/cc` escapes and re-entries,
  `dynamic-wind`, macro definitions, records, `parameterize`, `match`, strings,
  vectors, hash tables, SRFI-1, rest-only parameter lists, multiple values, and
  a dozen error cases (including a simple primitive that raises from compiled
  code) — **three ways**: compiled, compiled-to-bytes-and-back, and interpreted.
  All three have to print the same thing.
* The whole existing test suite (2611 assertions: the R7RS reference suite and
  every extension suite) is run **twice**, once per execution path, by
  `TestR7RSReferenceSuite` and `TestExtensionSuites`.  "Compiled" there means
  compiled: the suites are loaded through the same call the command line makes,
  so they exercise the compiler rather than the tree-walker twice.
* `cmd/goscheme/compiler_test.go` compiles a script, runs the `.scmc` file, and
  compares it with running the script; it also checks that a bytecode file with
  a mangled header is refused rather than read as Scheme.

## Performance

The panel in `internal/scheme/vm_panel_test.go` runs twelve shapes of program
both ways; the numbers below are the best of three `-benchtime 20x` runs on this
machine (a 12th-generation i3), and each one includes building the machine and
compiling the program, because that is what a run costs:

```sh
go test ./internal/scheme -run XXX -bench BenchmarkPanel -benchtime 20x -count 3
```

| Program | VM | interpreted | speed-up | VM allocated | interpreted | ratio |
|---|---|---|---|---|---|---|
| `lists` | 67.9 ms | 339.1 ms | **4.99×** | 29.9 MB | 273.1 MB | 0.11× |
| `higher-order` | 16.2 ms | 59.1 ms | **3.65×** | 13.3 MB | 67.1 MB | 0.20× |
| `mini-eval` | 15.5 ms | 55.2 ms | **3.56×** | 14.7 MB | 90.3 MB | 0.16× |
| `locals` | 53.7 ms | 191.3 ms | **3.56×** | 38.7 MB | 325.1 MB | 0.12× |
| `sort` | 7.7 ms | 26.4 ms | **3.43×** | 5.6 MB | 38.4 MB | 0.15× |
| `vectors` | 27.1 ms | 90.4 ms | **3.34×** | 14.7 MB | 173.9 MB | 0.08× |
| `tail-loop` | 47.0 ms | 154.4 ms | **3.28×** | 33.9 MB | 291.5 MB | 0.12× |
| `globals` | 54.5 ms | 175.9 ms | **3.23×** | 38.7 MB | 325.1 MB | 0.12× |
| `callcc` | 8.5 ms | 21.2 ms | **2.48×** | 5.2 MB | 28.1 MB | 0.19× |
| `fib` | 5.5 ms | 13.2 ms | **2.40×** | 6.7 MB | 24.4 MB | 0.27× |
| `closures` | 13.1 ms | 30.7 ms | **2.36×** | 21.4 MB | 54.2 MB | 0.40× |
| `strings` | 5.8 ms | 7.7 ms | **1.33×** | 39.1 MB | 43.0 MB | 0.91× |

**The VM is faster on all twelve, by 1.33× to 4.99×
(geometric mean 3.00×), and allocates 0.08× to
0.91× as much.**

Why it wins, in order of how much it matters:

1. **Bindings are resolved at compile time.** A variable reference is a
   (depth, slot) pair, where the interpreter walks an environment chain
   comparing symbols.  That is the `locals` and `globals` rows.
2. **Most calls allocate nothing.**  A call to a simple primitive is a Go call
   and a result value; a call to a compiled procedure saves the caller into one
   small frame and continues.  The interpreter, for the same call, builds an
   `Env`, operator and operand frames, and a `*Pair` per argument list.
3. **A frame keeps its own storage.**  Slots and the first four operands live
   inside the `vmEnv` and `fVM` structs, so an activation is one allocation
   rather than three, and resuming one usually allocates nothing at all.
4. **Macros are expanded once**, not on every evaluation of the form, and the
   shape of an expression is decided once instead of at every step.
5. **Special forms cost nothing at run time.** `cond`, `case`, `and`, `or` and
   the rest are jumps by then.

The honest caveats:

* `strings` is the thinnest margin (1.33×): the workload spends most of its
  time inside two or three big primitives, where both paths are doing the same
  Go work.
* These are microbenchmarks of specific shapes, not a suite of real programs;
  `mini-eval` and `sort` are the closest to "a real program" here, at 3.56× and
  3.43×.
* The comparison is against *this* interpreter.  A tree-walker that cached
  resolved bindings and pre-expanded macros would close part of the gap; the VM
  wins because those decisions are made once, ahead of time, which is also what
  makes the bytecode worth writing to a file.
* The interpreted numbers move by 5–15% between runs on this machine, so the
  ratios in a row are more trustworthy than the ratios between rows.

Where the remaining cost is, in the order it should be attacked:

1. a continuation frame is still allocated per non-tail call to a compiled
   procedure — 120 bytes — although nothing else about the call is;
2. global references look a symbol up in the environment each time, where a
   cached slot with a generation check would do (`mapaccess2_fast64` is the
   last non-GC symbol left in the profile);
3. a frame is a second allocation per call when it has more than four slots,
   and a boxed variable (`set!` on a parameter) is a third;
4. `+`, `car` and friends still go through a Go call with a `defer`/`recover`
   wrapper each, where an inline arithmetic instruction would not.

None of those changes the semantics, which is why they can be done later,
behind the tests.

## Limits

* There is no form the compiler declines.  A *body* is still all or nothing
  when it is compiled in one piece, which is why a file is compiled form by
  form (see the steps chunk above) — but every form itself is now something the
  compiler takes, so a file compiles whole.
* `goscheme build` binds the *compiled* program, so a bundled executable starts
  without reading source.  It falls back to binding the script when the build
  machine cannot compile it — a script that imports a library only present
  beside the executable at run time — and says so; the bundle format carries
  which of the two it holds.
* Compiling a form is not the same as compiling the *data* a program builds:
  `eval` and `load` still walk the tree, because the code they run does not
  exist until the program runs.  A lambda written as data has no bytecode until
  something evaluates the expression that makes it.
