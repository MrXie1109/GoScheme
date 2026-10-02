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

Measured on this machine (a 12th-generation i3), best of three runs, with the
programs in `/tmp` and the released binary:

| Program | Interpreted | Compiled |
|---|---|---|
| `(fib 27)` | 0.46 s | 0.47 s |
| a 3,000,000-turn tail loop | 2.70 s | 2.59 s |
| `go test -bench BenchmarkFib` | 15.2 ms | 15.6 ms |

So the VM is at **parity** today, not faster, and that is worth being plain
about.  What the bytecode buys now is the architecture: a program can be
compiled ahead of time and stored, the compiler's decisions are explicit, and
the hot path is a switch over a byte slice rather than a tree walk.

Where the remaining cost is, in the order it should be attacked:

1. a frame and its operand-stack copy are allocated per non-tail call
   (the interpreter keeps its first four operands in the frame itself);
2. `slots` and `vmEnv` are two allocations per call, where one would do;
3. global references look a symbol up in the environment each time, where an
   index into a global vector would do;
4. primitive calls go through the same generic `apply` as the interpreter, so
   `(+ a b)` is not yet an instruction.

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
