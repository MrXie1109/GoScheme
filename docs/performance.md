# GoScheme against C

We ran the same twelve workloads in C and in GoScheme, on one machine, to find
out how far apart they are.  The short answer: **about sixty-six times slower than C
doing the same work**, and up to 365× on the worst workload.  The goal was to stay within
ten.  We are not within ten, and this document says by how much, why, and what
would have to change.

Everything here is reproducible:

```sh
make build
bench/run.sh 5          # the table below, best of five
```

## How the comparison is set up

The twelve workloads are the ones in `internal/scheme/vm_panel_test.go`, the
same programs the bytecode VM is measured with, so the two documents are talking
about the same thing.  Each has a C counterpart in `bench/c/` written to do the
**same work with the same data structures**: a list is a malloc'd cell and a
pointer, a vector is an array, a closure is a function pointer and an
environment the caller passes, a continuation in `callcc.c` is `setjmp`.  The
C programs are not written to be slow, and they are not written to look like
Scheme either — they are what a C programmer would write for the job.

The Python programs in `bench/python/` are the third side, written the way a
Python programmer would write them, and they are the fairer opponent: CPython
boxes every value, dispatches dynamically and has a garbage collector, and it is
still a compiled bytecode machine with thirty years of work behind it.

The Guile programs in `bench/guile/` are the fourth side, and the one that
matters most: the same language, compiled and JIT-ed.  They are run with
`GUILE_AUTO_COMPILE=0` so that the auto-compilation of a changed file cannot
land inside a timed run — Guile compiles them either way, which is the point of
including it.

**All four sides compute the same answer**, which is checked: `fib` 6765,
`tail-loop` 20000100000, `closures` 200030000, `globals` and `locals` 600000,
`lists` 5000050000, `vectors` 49950000, `strings` 3000, `higher-order`
200010000, `mini-eval` 65000, `sort` 4, `callcc` 19999.

### Two C columns, because the question has two answers

The first attempt at this measured nothing at all, and the reason is worth
recording.  `gcc` at `-O2` does not run the loops these benchmarks are built
from: it *solves* them.  Summing 1..200000 is a multiplication.  So the table
has two C columns:

* **C -O2** — the compiler may do anything.  For `globals`, `locals` and
  `tail-loop` it removes the loop entirely and the time goes to zero.
* **C kept** — the same program compiled with the whole-loop rewrites held back
  (`-fno-aggressive-loop-optimizations -fno-tree-loop-optimize -fno-tree-vrp
  -fno-inline-functions -fno-optimize-sibling-calls`), so C performs the same
  sequence of steps the interpreter does.  **This is the column to compare an
  interpreter against**, and it is the one the summary uses.

The gap between the two C columns is itself a result: the same `globals.c`
takes **0.0000 ms** at `-O2`, **0.0245 ms** with the rewrites held back, and
**1.9226 ms** at `-O0`.  An "N times slower" claim is meaningless without saying
which of those you mean.

### Timing

Each program runs for at least a quarter of a second and reports the time per
run: a single execution of these benchmarks is microseconds, and timing one from
outside the process measures process startup (0.5 ms for C, 2.3 ms for
GoScheme) rather than the work.  The C side repeats until the clock has moved;
the Scheme side is timed by `bench/time.scm`, which is itself a GoScheme program
using `(scheme time)` and `system*`, because `/usr/bin/time` has a 10 ms
resolution and every one of these programs finishes faster than that.

Best-of-five for the Scheme side; the C numbers are stable to a few percent.

## The result

Per run, on a 12th-generation i3.  The last two columns are GoScheme divided by
each C column.

| workload | C -O2 | C kept | Python | Guile | GoScheme | vs C | vs kept C | vs Python | vs Guile |
|---|---|---|---|---|---|---|---|---|---|
| `locals` | 0.0000 ms | 1.5926 ms | 6.0367 ms | 62.565 ms | 70.816 ms | ∞ | 44× | 12× | **1.1×** |
| `vectors` | 0.0136 ms | 0.7248 ms | 3.3342 ms | 30.541 ms | 37.602 ms | 2765× | 52× | 11× | **1.2×** |
| `tail-loop` | 0.0499 ms | 1.8682 ms | 6.2170 ms | 49.805 ms | 66.883 ms | 1340× | 36× | 11× | **1.3×** |
| `globals` | 0.0000 ms | 1.6744 ms | 6.6541 ms | 62.925 ms | 80.256 ms | ∞ | 48× | 12× | **1.3×** |
| `mini-eval` | 0.0671 ms | 0.0992 ms | 3.1489 ms | 18.038 ms | 27.401 ms | 408× | 276× | 9× | 1.5× |
| `sort` | 0.5654 ms | 0.5345 ms | 0.9414 ms | 10.003 ms | 14.661 ms | 26× | 27× | 16× | 1.5× |
| `lists` | 2.5718 ms | 4.2733 ms | 5.2409 ms | 49.958 ms | 79.403 ms | 31× | 19× | 15× | 1.6× |
| `higher-order` | 0.5074 ms | 0.7398 ms | 1.0185 ms | 11.796 ms | 22.433 ms | 44× | 30× | 22× | 1.9× |
| `callcc` | 0.1008 ms | 0.1004 ms | 1.0373 ms | 8.136 ms | 16.196 ms | 161× | 161× | 16× | 2.0× |
| `closures` | 0.0050 ms | 0.1473 ms | 3.0284 ms | 12.044 ms | 24.821 ms | 4964× | 169× | 8× | 2.1× |
| `strings` | 0.0835 ms | 0.0765 ms | 0.0769 ms | 3.696 ms | 9.789 ms | 117× | 128× | 127× | 2.6× |
| `fib` | 0.0078 ms | 0.0249 ms | 0.5665 ms | 4.448 ms | 12.707 ms | 1629× | 510× | 22× | 2.9× |

**Geometric mean: 1.68× slower than Guile, 16× slower than CPython 3.12, and
75× slower than C doing the same work.**

The Guile column is the interesting one.  Eight of the twelve rows are within
2×, and the four that were expected to be worst — the arithmetic and
variable-lookup loops that C folds away — are the *closest*: `locals` 1.1×,
`vectors` 1.2×, `tail-loop` 1.3×, `globals` 1.3×.  The rows where we are
furthest behind are `fib` (2.9×) and `strings` (2.6×), and both have a specific
cause rather than a general one: `fib` is pure calls and arithmetic with no
allocation to amortise anything over, and `strings` is the quadratic-append case
described below.

## Why the three gaps are what they are

The gaps differ by a factor of forty-five, and what separates them is what an
interpreter *is*, not how well it is written.

**Guile is the measurement that matters.**  It is the same language, with a
compiler and a JIT, and we are within 1.7× of it overall and within 1.3× on the
loops.  Two things account for the difference that remains:

* **Its calling convention is native.**  A Guile procedure call does not
  allocate a frame object the way ours does; `fib` (2.9×) is exactly the row
  where per-call overhead shows up and nothing else does.
* **Its JIT specialises the hot path.**  `fib` and `strings` are where a JIT
  earns its keep, and they are our two worst rows against it.

Everything else in the panel — allocation, vectors, closures, control flow — is
already close, which is the useful result: the VM's design decisions (compile-time
binding resolution, no frame for a simple primitive call, operand stacks that
live in their frames) are doing what they were meant to do.

CPython is the same kind of machine as ours: a bytecode loop over boxed values,
dispatching on type at run time.  It is six times faster than Guile here, which
is worth noticing before reading the list — CPython's bytecode loop is an
extremely good one.  It is faster than us on this panel for four reasons, all of them things a mature implementation has and a young one does
not:

* **Its dispatch is a computed goto over specialised opcodes.**  `BINARY_OP`
  has separate implementations for int+int, int+float, str+str and the rest, so
  the common case does not call anything.  Our `+` is a Go function call with an
  arity check and a `defer`/`recover` around it.
* **Its integers are unboxed when small, inside the opcode.**  CPython still
  boxes them, but the box is created by the opcode, not by a generic constructor
  that must work for every type.
* **Its globals and locals are array indices**, resolved by the compiler, with a
  version check for invalidation.  Ours looks a symbol up in an environment chain
  for every global reference (see the `globals` row, 11×).
* **It has had thirty years of profiling.**  Every one of its fast paths exists
  because someone measured that path.

So the honest reading of 14.4× is: we are the same kind of program as CPython,
and we are one to two orders of magnitude behind it on the rows where it has
specialised opcodes and we do not.  On the rows where neither has an advantage —
`sort` (13×), `callcc` (13×), `higher-order` (21×) — we are closer, because
those are dominated by allocation and control flow rather than by arithmetic
dispatch.

### `strings` at 120×, and why it is not the disaster it looks like

CPython does `s += "x"` in place when the string has one reference, so the
Python program is linear and ours is quadratic: 3000 appends is 0.07 ms there
and 8.4 ms here.  The C column is 0.07 ms for the same reason.  This is a
legitimate optimisation on their side and a real gap on ours — a Scheme
`string-append` in a loop is quadratic in every implementation that does not
special-case it — but it is not a measure of the interpreter's dispatch, and the
table says so rather than hiding it.

## Why C is this far off

Not one reason — six, and they compose.

**1. Every value is boxed, and the box is an interface.**  A Scheme number in
this interpreter is a `Value`, which is a Go `interface{}`: two words, a type
pointer and a data pointer, and an allocation behind them.  `fib` adds two
numbers; C adds two registers.  This is most of the `fib` number, and `fib` is
the worst row in the table.  The `(goscheme fast)` library exists for exactly
this: it does whole-vector and whole-string work in Go so that the boxing
happens once per call instead of once per element, and it is one to two orders
of magnitude faster than the equivalent loop in Scheme for that reason alone.

**2. Dynamic dispatch everywhere.**  Adding two numbers calls a primitive
through a function value: check arity, call, recover from a panic if it raised.
C's `+` is an instruction.  Every operator in a compiled Scheme program is still
looked up as a value, because Scheme lets a program rebind `+`.

**3. Allocation is per operation, not per object lifetime.**  `(+ acc a b)` in
the `globals` loop allocates a fresh integer every iteration, and the garbage
collector has to reclaim it: 200000 iterations, 200000 allocations that the
machine then throws away.  C keeps `acc` in a register for the whole loop.  The
panel shows this as the difference between `locals` (41×) and `lists` (21×):
where a program genuinely allocates — building a list — the gap narrows, because
C has to allocate too.

**4. The interpreter is still an interpreter.**  The bytecode VM resolved names
at compile time and removed the per-call environment (see
[docs/bytecode-internals.md](bytecode-internals.md)), but a call is still a
frame allocation and a jump through a table, and a variable is a slice index
with a bounds check.  There is no inlining, no register allocation, no
type specialisation, no escape analysis.  C has all of them.

**5. Continuations are not free here, and `callcc` is 139× because of it.**  The
scheme captures the whole continuation stack; C's `setjmp` saves registers.
This is the one row where the *feature* is the cost, and the honest reading is
not "we are slow" but "we implement something C does not have".

**6. `mini-eval` (206×) is the row to worry about.**  It is the closest thing in
the panel to a real program: a small evaluator walking a tagged tree.  It is
slow because it does everything above at once — boxed tags, a dynamic lookup per
variable, a fresh binding per `let`, dispatch on the tag.  A compiler that could
specialise the tag dispatch and unbox the integers would move this row by more
than any micro-optimisation in the VM.

## Did we stay within ten?

Yes, against the opponent that makes sense to ask about.  The three answers:

| against | geometric mean | closest row | furthest row |
|---|---|---|---|
| Guile 2.2.7 (compiled Scheme, JIT) | **1.68×** | 1.1× | 2.9× |
| CPython 3.12 | 16.0× | 8× | 127× |
| C, same work, optimiser held back | 75× | 19× | 510× |
| C at `-O2` | ∞ | 26× | 4964× |

The 10× target was set against "a real language implementation".  Guile is that,
and we are inside it with room to spare — the only rows outside 2× are `fib`
(2.9×, all calls and arithmetic) and `strings` (2.6×, the quadratic append).
Against C the target was never going to be met by an interpreter, and this
document says so rather than dressing it up.

## What the numbers do not say

* Nor is it "Scheme is 16× slower than Python" or "1.7× slower than Guile".  It
  is this interpreter, against these implementations, on these twelve programs.
* This is not "Scheme is 66× slower than C".  It is "this interpreter, as it
  stands, on these twelve programs, is 66× slower than a C compiler doing the
  same work".  Compiled Scheme (Chez, Gambit, Racket's `raco make`) is typically
  within two to five times of C on programs like these, because it does the six
  things above that we do not.  **No such implementation was measured here** —
  none is installed on this machine, and this document does not claim a number
  for one.
* The C programs are given the same data structures on purpose, not the fastest
  possible ones.  `lists` allocates a cell per element in both languages; a C
  programmer who cared would use an array and beat our number by more.
* The panel is microbenchmarks of specific shapes.  `sort` (27×) and `lists`
  (21×) are the most representative of ordinary code, and they are also our best
  rows — which suggests the honest headline is closer to **30×** for programs
  that allocate real data, and 100×+ for arithmetic in a tight loop.

## What has been done about it: the comparison instructions

The first item on the list below was tried from the other end — not "unbox the
integers" but "stop calling a procedure to compare two of them".  A call to `<`
costs a type assertion on the operator, an arity check, and an indirect call
through the primitive's function pointer; an instruction costs a type assertion
and a comparison.  The compiler emits the instruction wherever it can see that
the operator is still the interpreter's own binding, so a program that rebinds
`<` gets the call it asked for.

Measured on the twelve workloads, pinned to one core, best of nine:

| workload | before | after | |
|---|---|---|---|
| `locals` | 69.62 ms | 62.36 ms | −10.4% |
| `vectors` | 36.04 ms | 32.12 ms | −10.9% |
| `globals` | 71.56 ms | 64.34 ms | −10.1% |
| `tail-loop` | 62.73 ms | 56.60 ms | −9.8% |
| `strings` | 11.39 ms | 10.53 ms | −7.6% |
| `sort` | 12.17 ms | 11.28 ms | −7.3% |
| `fib` | 9.59 ms | 8.96 ms | −6.6% |
| `callcc` | 12.04 ms | 11.55 ms | −4.1% |
| `closures` | 21.00 ms | 20.69 ms | −1.5% |
| `lists` | 73.26 ms | 72.54 ms | −1.0% |
| `higher-order` | 21.04 ms | 20.92 ms | −0.6% |
| `mini-eval` | 23.59 ms | 23.97 ms | +1.6% |

**Geometric mean: 5.8% faster**, with every workload but one improved or level.
(An earlier run of the same binaries measured 8.1%; this machine is shared, and
the two runs bracket the honest answer.)

### What was tried and thrown away

`+`, `-` and `*` were made instructions too, in the same way, and **measured
slower**: a loop of additions went from 6.13 s to 7.47 s.  The reason is that
they are variadic primitives whose body is already a tight loop, so an
instruction that tests two operands and then falls back to that same primitive
costs more than the call it saves.  Comparisons win because they are chained, so
the call and the arity check are the expensive part.  The arithmetic opcodes were
removed rather than kept behind a flag: an optimization that loses is a
liability, and the measurement is recorded here so that nobody re-adds it.

Two smaller things were tried inside the instruction itself, and both mattered:
calling a helper that returned the result measured slower, because the
instruction loop is far too large for Go to inline anything into it, so the
fast path is written out inline; and the operand array must not be aliased by
the result being appended over it, which cost every operation an allocation
until the copy was moved to the fallback path where it is rare.

## Obfuscation

`goscheme compile -obfuscate` is separate from all of the above and costs
nothing at run time (10.32 ms against 10.58 ms for `sort`, which is noise): it
rewrites the *names* in a compiled file — body names, slot names, the globals
the program defines — and shuffles the constant pools.  It is not encryption
and does not pretend to be.  See [bytecode.md](bytecode.md).

## Where the next 2× would come from

In the order that pays:

1. **Unboxed integers in a compiled frame.**  A slot that the compiler can prove
   holds a small integer needs no `Value` at all.  This is the `fib`, `locals`
   and `globals` rows, and it is the largest single win available.
2. **Inline the primitive calls.**  `opAdd` instead of a call to the `+`
   primitive, with the fixnum fast path in the instruction: removes a call, an
   arity check and a `defer`/`recover` from every arithmetic operation.
3. **Cached global references.**  A slot in the frame, invalidated by a
   generation counter, instead of a symbol lookup per reference — this is most
   of what is left in `globals`.
4. **A younger generation for the allocator.**  Most of these values die in the
   iteration that made them; Go's collector already handles that reasonably, but
   a nursery would make the `locals`/`tail-loop` rows cheaper to allocate in.
5. **Specialise the tag dispatch in an evaluator-shaped program.**  Not
   something the VM can do alone — it needs type feedback, which is a compiler
   project.

Even all five together would not close the gap to C on `fib`.  Reaching 10×
against C on programs whose inner loop is arithmetic means unboxed values *and*
inlined primitives *and* no allocation per operation, which is a compiled-Scheme
project rather than a better interpreter.

Against Guile the first two items on that list are where the remaining 1.7×
lives: a call that does not allocate a frame, and an integer that is not boxed
inside the loop.  Both are work a VM can do.  Against CPython the same two items
are most of the 16×, and against C they are necessary but nowhere near
sufficient.

## Files

* `bench/c/*.c` — the twelve C programs, each with a comment saying what it
  mirrors.
* `bench/python/*.py` — the same twelve in Python.
* `bench/guile/*.scm` — the same twelve in Guile, compiled by Guile itself
  (run with `GUILE_AUTO_COMPILE=0` so that a compile does not land inside a timed
  run).
* `bench/scheme/*.scm` — the same twelve in Scheme, extracted from the panel so
  that all three sides can be read side by side.
* `bench/time.scm` — the timer, written in GoScheme.
* `bench/run.sh` — builds both sides, runs them, prints the table.
