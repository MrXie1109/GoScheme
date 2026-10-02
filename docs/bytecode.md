# The bytecode VM

GoScheme runs a program two ways, and they are meant to be indistinguishable:

* **compiled** — a body the compiler understands becomes bytecode and runs on
  the stack machine in `internal/scheme/vm.go`;
* **interpreted** — the tree-walker in `internal/scheme/machine.go`, which is
  what everything ran on before this existed.

The compiler never imitates the interpreter for a form it cannot handle: it
*declines the whole body*, and that body runs interpreted.  The two paths
therefore agree by construction, and the tests are there to prove the
construction right — see [Guarantees](#guarantees) below.

```sh
goscheme script.scm                 # compiled where possible (the default)
goscheme -interp script.scm         # the tree-walker, for comparison
goscheme compile script.scm -o script.scmc
goscheme script.scmc                # run bytecode without reading source
```

## How it runs

`internal/scheme/compile.go` compiles one **body** at a time: a top-level form,
or a lambda body.  Inside a body it handles

* `quote`, `if`, `begin`, `lambda`, `set!`, `define` (top-level and internal),
* `let`, `let*`, `letrec`, `letrec*`, named `let`,
* `and`, `or`, `when`, `unless`, `cond`, `case` (including `=>`),
* applications, tail calls, and every self-evaluating literal.

plus anything a macro expands into, because **macros are expanded at compile
time** with the same expander the interpreter uses.  Everything else — `do`,
`guard`, `parameterize`, `dynamic-wind`, `define-record-type`, `match`,
`define-values`, `let-values`, `quasiquote`, `case-lambda`, `delay`, `include`,
`import` — makes the compiler decline that body, so it runs interpreted.

The compiler's table is a list of what it *does* handle rather than what it does
not, so adding a form is a local change to `special` in `compile.go`.

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
  That frame, like every interpreted frame, is written once and never mutated —
  a resumption builds a new one with its own copy of the operand stack.  So a
  continuation captured by `call/cc` inside compiled code can be invoked any
  number of times, and it composes with `dynamic-wind` and `guard` because
  those are the machine's frames, not the VM's.

## Bytecode files

`goscheme compile` writes a **`.scmc`** file: a magic number, a version, and the
program as chunks.  A chunk is either compiled code or a source form, and the
source ones are exactly the forms that teach the compiler something
(`import`, `define-syntax`, `include`) or that it could not compile — which is
why loading a `.scmc` file still applies its imports and still defines its
macros, while the rest of the program is never parsed again.

```
GSCM 01 | chunk count | chunk ... 
chunk   = 0 <datum>            ; a source form to evaluate
        | 1 <code>             ; bytecode to run
code    = name | instructions | constants | frame shape | parameters
datum   = tagged and recursive: numbers (exact, rational, inexact), strings,
          symbols (interned on load), characters, booleans, pairs, vectors,
          bytevectors, and nested code
```

Consecutive ordinary forms become **one chunk holding a `begin`**, because that
is what running the same file does: a continuation captured in one top-level
form has to span the rest of the program, and a chunk boundary would end it.

A `.scmc` file is not portable across versions: the loader refuses a version it
does not speak rather than misreading it.

## Guarantees

The two paths are held together by tests, not by hope:

* `internal/scheme/vm_test.go` runs a corpus of 33 programs — arithmetic,
  closures and `set!`, internal definitions, every compiled derived form, tail
  calls, `call/cc` escapes and re-entries, `dynamic-wind`, macro definitions,
  records, `parameterize`, `match`, strings, vectors, hash tables, SRFI-1, and
  a dozen error cases — **three ways**: compiled, compiled-to-bytes-and-back,
  and interpreted.  All three have to print the same thing.
* The whole existing test suite (2611 assertions: the R7RS reference suite and
  every extension suite) is run **twice**, once per execution path, by
  `TestR7RSReferenceSuite` and `TestExtensionSuites`.
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
| `callcc` | 16.7 ms | 23.5 ms | **1.41×** | 14.5 MB | 28.1 MB | 0.52× |
| `locals` | 169.9 ms | 211.4 ms | **1.24×** | 179.5 MB | 325.1 MB | 0.55× |
| `sort` | 25.1 ms | 30.2 ms | **1.20×** | 26.3 MB | 38.4 MB | 0.69× |
| `higher-order` | 55.5 ms | 66.0 ms | **1.19×** | 43.1 MB | 67.1 MB | 0.64× |
| `lists` | 291.6 ms | 342.9 ms | **1.18×** | 185.2 MB | 273.2 MB | 0.68× |
| `globals` | 168.2 ms | 194.9 ms | **1.16×** | 179.5 MB | 325.1 MB | 0.55× |
| `closures` | 31.9 ms | 35.3 ms | **1.11×** | 37.4 MB | 54.2 MB | 0.69× |
| `mini-eval` | 60.4 ms | 66.8 ms | **1.11×** | 56.8 MB | 90.3 MB | 0.63× |
| `tail-loop` | 166.9 ms | 184.4 ms | **1.10×** | 174.8 MB | 291.6 MB | 0.60× |
| `vectors` | 97.6 ms | 107.2 ms | **1.10×** | 106.5 MB | 173.9 MB | 0.61× |
| `strings` | 9.5 ms | 10.3 ms | **1.09×** | 41.2 MB | 43.0 MB | 0.96× |
| `fib` | 15.1 ms | 15.2 ms | **1.01×** | 16.5 MB | 24.4 MB | 0.68× |

**The VM is faster on all twelve, by 1.01× to 1.41×
(geometric mean 1.15×), and allocates 0.52× to
0.96× as much.**

Why it wins, in order of how much it matters:

1. **Bindings are resolved at compile time.** A variable reference is a
   (depth, slot) pair, where the interpreter walks an environment chain
   comparing symbols.  That is the `locals` and `globals` rows.
2. **Macros are expanded once**, not on every evaluation of the form, and the
   shape of an expression is decided once instead of at every step.
3. **A compiled call allocates less than an interpreted one**: no `Env` object,
   no operator/operand frames — a frame slice and a continuation, and the
   continuation is only a slice copy when a call is pending.
4. **Special forms cost nothing at run time.** `cond`, `case`, `and`, `or` and
   the rest are jumps by then.

The honest caveats:

* `fib` is the thinnest margin (1.01×) because it is nothing but calls and
  arithmetic: both paths allocate about one frame per call, and that dominates.
* These are microbenchmarks of specific shapes, not a suite of real programs;
  `mini-eval` and `sort` are the closest to "a real program" here, at 1.11× and
  1.20×.
* The comparison is against *this* interpreter.  A tree-walker that cached
  resolved bindings and pre-expanded macros would close much of the gap; the VM
  wins because those decisions are made once, ahead of time, which is also what
  makes the bytecode worth writing to a file.

Where the remaining cost is, in the order it should be attacked:

1. a frame and its operand-stack copy are allocated per non-tail call (the
   interpreter keeps its first four operands inside the frame itself);
2. `slots` and `vmEnv` are two allocations per call where one would do;
3. a primitive call goes through the same generic `apply` as the interpreter,
   so `(+ a b)` is not yet an instruction of its own;
4. global references look a symbol up in the environment each time, where a
   cached slot with a generation check would do.

None of those changes the semantics, which is why they can be done later,
behind the tests.

## Limits

* A body that uses a form outside the compiler's table runs interpreted, and so
  do the lambdas written inside it.  `do` is deliberately in that list: its
  `(continue)` is implemented with a guard the interpreter installs, and
  compiling `do` would quietly change what `(continue)` means.
* `case-lambda` clauses are interpreted for now, although each clause could be
  compiled independently.
* `goscheme build` still embeds a script as source; embedding the compiled
  program instead would let a standalone executable start without parsing,
  which is the obvious next step.
