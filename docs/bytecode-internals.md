# The bytecode VM in detail

[docs/bytecode.md](bytecode.md) is the overview: what compiles, what does not,
what the two execution paths guarantee, and how to use them.  This is the
reference underneath it — the file format byte by byte, the instruction set,
what the machine does when it runs a call, and the measured difference between
running bytecode and walking the tree.

Everything here is checkable against the code: the writer and reader are
`internal/scheme/bytecode.go`, the instruction loop and the frames are
`internal/scheme/vm.go`, and the compiler that produces it all is
`internal/scheme/compile.go`.

## The file format

A `.scmc` file is a header and a list of chunks.  It is written by
`WriteBytecode` and read by `ReadBytecode`.

```
GSCM | version u8 | chunk count uvarint | chunk ...
```

* the magic is four bytes, `G S C M`;
* the version is **3** (version 2 added the steps chunk, version 1 had neither
  that nor the primitive reference; every older file is still read, and a
  version from the future is refused rather than misread);
* then one chunk per top-level part of the program, in source order.

### The primitives

Every field below is one of these:

| encoding | meaning |
|---|---|
| `u8` | one byte |
| `uvarint` | Go's `encoding/binary` unsigned varint |
| `svarint` | Go's `encoding/binary` signed varint (zig-zag) |
| `u64` | eight bytes, little-endian |
| `str` | `uvarint` length, then that many raw bytes |
| `datum` | a tagged value, below |
| `bits` | bit set, least-significant bit first, padded to a whole byte |

`bits` is how the per-slot flags of a frame are stored: bit *i* of the set is
bit *i%8* of byte *i/8*, and the last byte is zero-padded even when the
declared count is not a multiple of eight.

### Chunks

```
chunk = 0 datum            ; a source form to evaluate
      | 1 code             ; bytecode to run
      | 2 uvarint chunk... ; a run of chunks that share one extent
```

* **0** is a form the compiler declined, or a form that teaches the compiler
  something — an `import`, a `define-syntax`, an `include`.  It is evaluated by
  the tree-walker when the file is loaded.
* **1** is a compiled body.
* **2** is a run of chunks that must run in **one continuation extent**, which
  is what a file's ordinary top-level forms need: a continuation captured in
  one of them outlives the ones after it.  The count is at least two (a run of
  one is written as that chunk), and runs do not nest.

### A compiled body

```
code = str name
       uvarint instruction count
       instruction...
       uvarint constant count
       datum...              ; the constant pool
       uvarint slot count
       bits                  ; which slots hold a cell (set! variables)
       bits                  ; which slots must be initialised before use
       uvarint parameter count
       u8                    ; 1 when there is a rest parameter
       uvarint rest slot
       uvarint name count
       (u8 str)...           ; the name of each slot, for error messages
```

```
instruction = u8 opcode | svarint arg1 | svarint arg2
```

Every instruction carries both operands whether it uses them or not; unused
ones are written as zero.  That costs a byte or two per instruction and makes
the reader and the disassembly trivial.

The parameter *symbols* are deliberately not stored: a compiled clause never
binds them in an environment, and they exist for arity and for the messages
the interpreter prints, so the loader makes placeholders again.  The slot
*names* are stored, because a program that reads an uninitialised variable is
told which one.

### Data

| tag | value | what follows |
|---|---|---|
| 0 | the empty list | — |
| 1 | `#t` | — |
| 2 | `#f` | — |
| 3 | the unspecified value | — |
| 4 | the unassigned marker | — |
| 5 | character | `uvarint` code point |
| 6 | exact integer | `str` decimal digits (arbitrary precision) |
| 7 | exact rational | `str` `numerator/denominator` |
| 8 | inexact real | `u64` IEEE-754 bits |
| 9 | complex | `datum` real, `datum` imaginary |
| 10 | string | `str` its characters |
| 11 | symbol | `str` its name, interned on load |
| 12 | pair | `datum` car, `datum` cdr |
| 13 | vector | `uvarint` length, `datum` each element |
| 14 | bytevector | `uvarint` length, that many raw bytes |
| 15 | compiled body | `code` |
| 16 | end-of-file object | — |
| 17 | a runtime helper | `str` its name, resolved in the interpreter's own table |

Integers are decimal text rather than binary because they are the only value
that can be arbitrarily large, and `big.Int` already speaks decimal; a symbol is
stored by name and **interned on load**, because symbols are compared by
pointer everywhere else in the interpreter.

Tag 17 is how a compiled program refers to one of the interpreter's own
runtime helpers — the guard helper, for one — by name.  Only the helpers in
`internalPrimitives` can be written, and a program can neither see them nor
rebind them, which is the point: a compiled `guard` must reach the same helper
whatever the program has done to its own names.  Nothing else may put a
primitive in a constant pool.

Cyclic data is refused, with an error rather than a wrong file: a literal that
refers to itself cannot come from the reader, but a hand-written file could try.

### What is refused

A file is rejected, not guessed at, when the magic is wrong, when the version
is one this interpreter does not speak, when it is truncated, when a chunk tag
is not 0, 1 or 2, when a steps run has fewer than two steps or nests inside
another run, when an opcode is one this interpreter does not have (the
instruction loop's switch has no default case, so an unknown one would be a
silent no-op), when a datum tag is unknown, or when data is cyclic.  A `.scmc`
whose header cannot be read is never treated as source.

## The instruction set

Twenty-three opcodes.  `—` is an empty operand stack for that instruction.

| opcode | operands | stack | what it does |
|---|---|---|---|
| `opConst` | constant index | — → v | push a literal |
| `opLocal` | depth, slot | — → v | push a local from an enclosing frame |
| `opLocalCell` | depth, slot | — → v | the same, through its cell |
| `opLocalCheck` | depth, slot | — → v | the same, error if uninitialised |
| `opLocalCellCheck` | depth, slot | — → v | both of the above |
| `opSetLocal` | depth, slot | v → — | store a local |
| `opSetCell` | depth, slot | v → — | store through its cell |
| `opNewCell` | depth, slot | v → — | store, wrapping the value in a fresh cell |
| `opGlobal` | symbol constant | — → v | look a name up, error if unbound or uninitialised |
| `opSetGlobal` | symbol constant | v → u | `set!`, then push the unspecified value |
| `opDefineGlobal` | symbol constant | v → u | `define`, then push the unspecified value |
| `opClosure` | Code constant | — → p | a procedure over that body, capturing this frame |
| `opInterpClosure` | `lambda` source constant | — → p | a procedure the compiler left to the interpreter |
| `opPop` | — | v → — | discard |
| `opEqv` | — | a b → v | `(eqv? a b)`, which is what `case` compares |
| `opJump` | target | — | go to the target |
| `opJumpFalse` | target | v → — | jump when the test is false |
| `opJumpTrue` | target | v → — | jump when it is true |
| `opJumpFalseKeep` | target | v → v | jump when false, leaving the test: `and`, `or` |
| `opJumpTrueKeep` | target | v → v | jump when true, leaving the test: `cond =>` |
| `opCall` | argument count | p a… → v † | call, and come back here |
| `opTailCall` | argument count | p a… → — | call, discarding this activation |
| `opReturn` | — | v → — | return v from this activation |

Targets are absolute instruction indices in the same body; the compiler patches
them as it goes, so a jump is one varint and no second pass.

† `opCall` leaves the value on the stack immediately when the callee is a simple
primitive, and when the activation is resumed otherwise; either way the
instruction after the call sees exactly one more value than before it.

## How the machine runs

### The state of a run

`vmRun` is a loop with a *current activation*:

```go
code, ip, env, globals, vals
```

`vals` is the operand stack, a `[]Value` this activation owns.  `env` is its
frame, `globals` the environment its closure was defined in — which is where
`opGlobal` and friends look names up — and `ip` where it is in `code`.

### Frames

A frame is a `vmEnv`:

* `parent`, the frame of the closure that created this one, which is what
  `(depth, slot)` addressing walks;
* `slots`, the variables.  Up to four of them live **inside the struct**
  (`buf`), so the common activation is one allocation of 96 bytes instead of
  two; a bigger frame allocates a slice;
* a variable that `set!` can change lives in a **cell** (`Boxed` in the file
  format), so that a closure and the frame it came from see the same binding;
* a variable introduced by an internal `define` or by `letrec` is **checked**
  (`Checked`), and reading it before its initialiser is an error, as in the
  interpreter.

### Calls

`opCall` looks at what it is calling and does one of three things.

**A simple primitive.**  That means a primitive registered through `defSimple`
(596 of the 644 builtins): a Go function with no access to the machine, which
can therefore only return a value or raise.  It is called inline, with no
continuation frame at all, and its result is pushed straight onto the operand
stack.  This is what makes `(+ a b)` in a loop a call that allocates nothing.

**A compiled procedure.**  The caller's state is saved into an `fVM` (below),
that frame is pushed, and the callee's body *becomes the current activation in
the same Go frame*: `code, ip, env, globals` are replaced and the loop
continues.  The operand array is handed over too, because everything that
mattered was copied out of it first.

**Anything else** — an interpreted closure, a `call/cc` continuation, a
parameter, a multi-clause or `case-lambda` procedure, or a primitive that calls
back into Scheme: the frame is pushed and `m.apply` runs it, which is the
interpreter's own procedure-application path.

A `raise` from a simple primitive is the interesting case: the call has already
taken its arguments off the stack, and the frame that would have been pushed
before the call is built *now*, at the raise point.  That is indistinguishable
from the ordinary path, because a simple primitive pushes no frames of its own
— and it is what lets `guard` and continuing handlers see the right stack.

### Returning, and the trampoline

`opReturn` pops a value, calls `m.Return(v)` — which only records the value and
a flag — and returns from `vmRun`.  Control goes back to the machine's
`runLoop`, which pops the frame on top of the continuation stack and resumes it
(see below).

That is the whole reason the Go stack stays shallow.  A call to a compiled
procedure does not nest a Go call, and a return does not unwind one through a
chain of activations: every activation either replaces the current one or
suspends back to `runLoop`.  A non-tail recursion a million deep costs a
million frames on the *Scheme* stack and **two** Go ones — measured, by running
one under a 3 MB Go stack cap.  A tail call is the same replacement with nothing saved,
so it costs neither — `opTailCall` never pushes a frame, which is what makes
tail calls proper.

### Suspending and resuming

An `fVM` is the continuation of a compiled activation that is waiting for a
call:

```go
type fVM struct { code *Code; ip int; env *vmEnv; globals *Env; vals []Value
                  buf [4]Value }
```

It holds the instruction to resume at, the frame, and the operand stack below
the call — 120 bytes in all, four operands of which are inside the struct.  Like every frame in this machine it is written once and never
mutated, which is what lets a continuation captured above it be invoked any
number of times.  The short operand stack lives inside the struct (`buf`), and
`resume` appends the call's value to it in place — **unless a continuation has
ever been captured** (`Machine.framesCopied` is set by `call/cc` and by
`guard`), in which case the frame may be reachable from more than one stack and
a fresh slice is made instead.  Programs that never capture a continuation
never pay for that copy, and programs that do get the semantics the report
requires.

A frame is copied, not shared, when its activation suspends: everything below
the call is copied into `buf` (or a slice, if it is longer than four) so that
the caller's array belongs to the caller again.  What is *shared* between
continuations is exactly what Scheme says is shared: the cells of variables
that `set!` can change.

### Macros in a body, and the shadow environment

A macro defined at the top level is expanded with the global environment, which
the compiler has.  One defined in a *body* — an internal `define-syntax`, a
`let-syntax` — is expanded with the environment of that body, which a compiled
body does not have: its variables are slots.  The compiler therefore keeps a
shadow environment beside every scope: a real `Env`, with the same names, whose
values are never read.  It exists so that a mark on a template's identifier
names a scope, the block for that scope holds the same names, and the slot for
one of them is what the reference compiles to.  Nothing is in an environment at
run time, and hygiene is the interpreter's for every macro whose definition
scope encloses its use.

### The boundary with the interpreter

The VM is not a separate interpreter: it is the same machine with a second way
to run a body.  `m.stack` is the machine's continuation stack, `m.Return` and
`m.apply` are the machine's, and `dynamic-wind`, `guard` and parameters are
machine frames — which is why a continuation captured inside compiled code
composes with them.  When the compiler declines a body, that body is evaluated
by the tree-walker, and the compiled code that called into it sees an ordinary
procedure call.  A group the compiler could not take whole is compiled form by
form and run as a `Steps` chunk, so one `do` in a file does not send the file
to the tree-walker (see [bytecode.md](bytecode.md)).

## Efficiency

Twelve workloads, both paths, best of three `-benchtime 20x` runs on the
machine these notes were written on (a 12th-generation i3).  Every figure
includes building the machine and compiling the program, because that is what a
run costs.  The panel is `internal/scheme/vm_panel_test.go`:

```sh
go test ./internal/scheme -run XXX -bench BenchmarkPanel -benchtime 20x -count 3
```

| workload | bytecode | tree-walker | | bytecode allocated | ratio |
|---|---|---|---|---|---|
| `lists` | 67.9 ms | 339.1 ms | **4.99×** | 29.9 MB | 0.11× |
| `higher-order` | 16.2 ms | 59.1 ms | **3.65×** | 13.3 MB | 0.20× |
| `mini-eval` | 15.5 ms | 55.2 ms | **3.56×** | 14.7 MB | 0.16× |
| `locals` | 53.7 ms | 191.3 ms | **3.56×** | 38.7 MB | 0.12× |
| `sort` | 7.7 ms | 26.4 ms | **3.43×** | 5.6 MB | 0.15× |
| `vectors` | 27.1 ms | 90.4 ms | **3.34×** | 14.7 MB | 0.08× |
| `tail-loop` | 47.0 ms | 154.4 ms | **3.28×** | 33.9 MB | 0.12× |
| `globals` | 54.5 ms | 175.9 ms | **3.23×** | 38.7 MB | 0.12× |
| `callcc` | 8.5 ms | 21.2 ms | **2.48×** | 5.2 MB | 0.19× |
| `fib` | 5.5 ms | 13.2 ms | **2.40×** | 6.7 MB | 0.27× |
| `closures` | 13.1 ms | 30.7 ms | **2.36×** | 21.4 MB | 0.40× |
| `strings` | 5.8 ms | 7.7 ms | **1.33×** | 39.1 MB | 0.91× |

Geometric mean **3.0× faster**, **82% fewer bytes allocated**.

Where the difference comes from, in the order it matters:

1. **Names are resolved at compile time.**  A reference is a `(depth, slot)`
   pair or a constant index, where the tree-walker walks an environment chain
   comparing symbol pointers at run time.  This is most of `locals` and
   `globals`.
2. **Most calls allocate nothing.**  A simple primitive is a Go call and a
   result value; a call to a compiled procedure is one small frame and a
   handed-over operand array.  The interpreter, for the same call, builds an
   `Env`, operator and operand frames, and a `*Pair` per argument list.
3. **A frame keeps its own storage** — up to four slots in the `vmEnv`, up to
   four operands in the `fVM` — so the common activation is one allocation
   instead of four, and the common resumption allocates nothing.
4. **Macros are expanded once**, at compile time, instead of on every
   evaluation, and the shape of an expression is decided once rather than at
   every step.
5. **Special forms cost nothing when they run.**  `cond`, `case`, `and` and
   `or` are jumps by then.

The honest caveats:

* `strings` is the thinnest margin (1.33×): that workload spends its time
  inside two or three large primitives, where both paths do the same Go work.
* These are microbenchmarks of specific shapes, not a suite of real programs.
  The two closest to "a real program" are `mini-eval` and `sort`, at 3.56× and
  3.43×.
* The comparison is with *this* tree-walker.  One that cached resolved bindings
  and pre-expanded macros would close part of the gap; the VM wins because
  those decisions are made once, ahead of time, which is also what makes the
  bytecode worth writing to a file.
* The interpreted column moves by 5–15% between runs on this machine, so a row's
  ratio is more trustworthy than the ratio between rows.

Where the remaining cost is, in the order it should be attacked:

1. a continuation frame is still allocated per non-tail call to a compiled
   procedure — 120 bytes — though nothing else about the call is;
2. a global reference looks its symbol up in the environment every time, where
   a cached slot with a generation check would do;
3. a frame is a second allocation when it has more than four slots, and a
   `set!` variable in a parameter is a third;
4. `+`, `car` and the rest still go through a Go call with a recovery wrapper,
   where an inline arithmetic instruction would not.
