# Compiler notes: what was measured, and what to do next

This is the working record behind [docs/compile.md](compile.md) and
[docs/performance.md](performance.md). Those two say what the compiler *is*;
this one says how it got there — including the things that were tried and
thrown away, and the one piece of work that is understood but not done.

It exists because the most expensive mistakes in this compiler were all the same
mistake: **a rule that was reasoned about instead of measured**, and a
measurement that compared two things it had no right to compare.

## What landed

### Mutual recursion compiles

Two procedures that call each other used to be dropped, with the reasoning that
"two procedures that call each other cannot both be defined first, and LLVM will
not accept either ordering". The premise is false: LLVM resolves a forward
reference to a function in the same module. The topological order the generator
walks is its own bookkeeping, and a cycle is a case it was never asked to handle
rather than a case machine code cannot express.

`even2?`/`odd2?` are two tail calls to each other, which is what tail calls
exist for:

| 5,000,000 mutual calls | time |
|---|---|
| compiled | 0.004 s |
| interpreted | 0.713 s |

Constant stack, both `musttail`s present.

### A `letrec*` that refers forward compiles, with the semantics it has

The old code rewrote `letrec*` into `let*` and refused the shape where that is
wrong — a forward reference. Refusing was *correct*: the rewrite really would
have been wrong, and it was wrong in a way that crashed rather than a way that
was slow.

The fix was not to relax the check but to give the form its actual meaning.
Every binding gets a heap **cell** before any initialiser runs, so a lambda
created by an earlier initialiser captures the *location* of a name bound later:

```scheme
(letrec* ((mean (lambda (f g) (f (/ (sum g ton) n))))   ; sum and n come later
          (sum  (lambda (g ton) ...))
          (n    (sum (lambda (x) 1) ton)))
  ...)
```

This is the R7RS suite's `means`, and it now agrees with the interpreter exactly:
`(27 9.728000255822641 1800/497)`.

It is what the interpreter gets for free by sharing one environment frame that
later bindings are added to. A compiled frame's slots are on the C stack and are
gone when the procedure returns, so the location has to be on the heap — which
is what the cell is.

### A constant is built once, not once per iteration

A literal is a constant: the same `"hello"` denotes the same object for the life
of the program. It was being built where it was written, which for a loop meant
reading the source text back, parsing it and allocating a fresh string **per
iteration**.

`main` now builds each one once into a module-level `%gs.val` global, and every
use loads two words from static storage. The crossing count in

```scheme
(define (loop i acc)
  (if (= i 0) acc (loop (- i 1) (+ acc (string-length "hello")))))
```

went from four per turn to three.

The order in `main` is the whole difficulty. Emitting a *body* is what discovers
which literals need a global, so the fill has to come after the bodies are
emitted and before the top-level forms, because those are what call the bodies.
Written at the end of `main` after the forms, every body read a zero-initialised
global and `string-length` answered "expected a string but got 0" once per
iteration.

### The call cache was reset every iteration, so it cached nothing

Found by asking why a loop that computes one number used 209 MB.

A runtime call site caches what its name resolved to. The cache was a **stack
slot**:

```llvm
entry:
  %cache3 = alloca i64
  store i64 0, i64* %cache3     ; cleared on every entry
```

A loop written as tail recursion re-enters its function through `entry` every
iteration, so the slot was cleared every iteration and the lookup happened every
iteration. It was worse than a wasted lookup: the runtime caches a resolved
procedure by *storing it in its value table*, and that table is append-only.

| iterations | table size before | after |
|---|---|---|
| 300000 | 300002 | **3** |
| 3000000 | 3000002 | **3** |
| 3000000, no call in the loop | 1 | 1 |

The slot is now a module-level global, which is what "once per site" has to mean
for a site inside a function entered many times.
`TestTheCallCacheIsAGlobal` fails if it goes back to being a stack slot.

### A declined callee is promoted when its caller is compiled

The cost rule asks whether compiling a body pays **for itself**. For

```scheme
(define (helper l) (if (null? l) 0 (string-length "x")))
```

the answer is no, and declining it is right. But a procedure whose callee had no
native body was *refused*, so a compiled caller could not exist without it:

| configuration | `helper` | `caller` | 3,000,000 iterations |
|---|---|---|---|
| the rule taken literally | interpreted | refused | 2.58 s |
| caller compiled, helper interpreted | interpreted | compiled | **2.59 s** |
| both compiled | compiled | compiled | **1.19 s** |

The middle row decides it. Compiling `caller` alone changes nothing, because the
cost lands on the *caller's* side of the boundary, and that is what the rule
cannot see from the callee's body.

So a declined procedure is now **postponed** rather than forgotten and promoted
if a compiled procedure calls it.

**Promotion is not unconditional.** The first version promoted everything, and
the panel caught it immediately: `mini-eval` dropped to 0.42× with `native=4`.
`ev` is a tree walk crossing on `car`, `cdr`, `assq`, `cadr` and `caddr`, and
compiling it makes *it* slower. The discriminator is what compiling the callee
costs against the one crossing the promotion removes (`promotionGain = 8`):
`helper` costs 4 and is promoted, `ev` costs 39 and is not.

### Small things, same session

- The `Sync`-primitive fast path in `ApplySync` (`car`, `cdr`, `string-length`
  and the rest carry `Primitive.Sync`, and the VM has called them directly for a
  long time). **Measured as nothing** — 0.4842 s against 0.4844 s for a loop
  whose only work is `string-length` — and reverted. What it showed is that a
  crossing's ~138 ns is the cgo boundary itself, not `runLoop`'s dispatch, so the
  only way to remove a crossing is to not make one.
- Two `-compile-all`-only bugs that the constant work exposed: `arith.fast`
  opened a block where the previous one had already ended (`opt: expected
  instruction opcode`), and a named let whose body is not a walk fell through to
  the generic emitter and reported its loop variables as undefined.
- Emission failure now cascades **before** anything is written. A procedure's
  nested lambdas are emitted as part of its body, so dropping a failed callee
  afterwards left a direct call to a symbol that did not exist, and `opt` rejects
  the module — costing the program every native body it had.

## What not to do

**Do not remove the cost rule.** It was disabled outright and the panel run
again: seven programs gained and five lost, which reads like an argument for
deleting it until the two losses are looked at — `strings` at 0.50× and
`mini-eval` at **0.27×**. Those are the shape the rule exists to catch.
`strings`' `build` is `(string-append acc "x")`, where the call is the entire
work; `mini-eval`'s `ev` is a tree walk whose work is `car` and `cdr`, and every
one of them becomes a crossing.

Two refined versions were written and both were deleted, because neither could be
shown to beat the simpler rule:

1. **A ratio of native operations to crossings.** Recovered `mini-eval` (0.99×,
   declined) and held `strings` at 0.80×, but declined `sort`'s `build` — two
   native ops per `cons` — and `sort` fell from 3.3× to 1.2×.
2. **Dropping the "it does no arithmetic of its own" clause.** Made `sort`
   *worse*: 1.45× down to 0.76×, because `msort`'s body is
   `(cons (car a) (merge (cdr a) b))` and compiling it turns three cheap
   interpreter operations into three crossings.

Both rules were then run seven times each and the difference disappeared — every
row moved by more than the gap. The apparent wins were run-to-run variance on
programs that finish in ten milliseconds.

## What is understood and not done: unwinding through a compiled frame

This is the one piece of work with a known shape, so it is written down rather
than left as a puzzle.

`raise`/`call/cc`/`dynamic-wind`/`guard` are **refused**, and a body that
mentions one runs in the interpreter. A compiled body is a machine function with
its own frame and no continuation on the interpreter's stack, so a transfer that
has to return into it — or escape past several of them — has nothing to return
to.

**The mechanism that solves it is LLVM's SJLJ exception handling.** From
[Exception Handling in LLVM](http://releases-origin.llvm.org/3.2/docs/ExceptionHandling.html):

> For each function which does exception processing — be it try/catch blocks or
> cleanups — that function registers itself on a global frame list. When
> exceptions are unwinding, the runtime uses this list to identify which
> functions need processing. [...] The runtime returns to the function via
> `llvm.eh.sjlj.longjmp`, where a switch table transfers control to the
> appropriate **landing pad**.

A landing pad "corresponds roughly to the code found in the catch portion of a
try/catch sequence".

A version of this was built: each body with a checked call announced itself on
entry, checked on the way out, and returned the handler's value from a landing
pad. **It works for a raise that crosses one compiled frame and loses the
handler's value across three.**

The diagnosis went as far as this: `opt -O2` merges an inlined chain's checks —
one frame where the emitter made three — so a scheme keyed on a *count* has
nothing left to count, and no frame can tell whether it is the last. `noinline`
on those bodies did **not** fix it, which means the interaction is not only
inlining and was not found. The work was reverted rather than left half-working:
a refusal is a slower program, and a wrong answer is a wrong one.

What it would take:

1. **A real frame chain** — the doc's actual scheme, which the runtime walks —
   rather than a count of frames.
2. **Or LLVM's own `invoke`/`landingpad`**, which brings a personality function
   and the Itanium ABI with it.

Note what does *not* need it: a `raise` in **tail position** in a compiled body
is correct (it crosses into the runtime to find its handler, and the runtime has
the handler stack), and so is a `guard` whose body merely crosses, because
`emitGuard` emits it as a thunk the runtime runs. What fails is a `raise` in
**non-tail position** where the handler lies outside more than one compiled
frame. Telling those apart needs an escape analysis this generator does not
have.

`call/cc` is further away still: it needs re-entrant continuations, which is
stack copying or CPS.

## The lesson, stated once

Three separate measurement mistakes were made in this session and all three had
the same shape:

- **`fib` at 3.4×** was stale and never re-measured; the real figure is 157×.
- **"0.84 s → 0.46 s, 1.87×"** for the Sync fast path was two binaries linked
  against different runtimes. The numbers came from different code paths and
  compared nothing. Every number in this file now comes from `-static`, from the
  same `.ll`, changing only the runtime.
- **The `helper`/`caller` promotion** looked like a 2× win until the middle row
  of that table was measured, which showed that compiling the caller alone does
  nothing at all.

The verification standard that caught every real bug is **comparing the two
engines' output** — interpreted against compiled — not reading the generated
code. Every one of these was found that way:
`TestCompiledProgramAgreesWithTheInterpreter`, the `-compile-all` variant, and
`bench/run-native.sh`.
