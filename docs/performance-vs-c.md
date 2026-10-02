# GoScheme against C

We ran the same twelve workloads in C and in GoScheme, on one machine, to find
out how far apart they are.  The short answer: **about sixty-six times slower than C
doing the same work**, and up to 365× on the worst workload.  The goal was to stay within
ten.  We are not within ten, and this document says by how much, why, and what
would have to change.

Everything here is reproducible:

```sh
make build
bench/run.sh 5          # the table below
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

Both sides compute the same answer, which is checked: `fib` 6765, `tail-loop`
20000100000, `lists` 5000050000, `sort` 4, `callcc` 19999, and so on for all
twelve.

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

| workload | C -O2 | C kept | GoScheme | vs -O2 | vs kept |
|---|---|---|---|---|---|
| `fib` | 0.0069 ms | 0.0237 ms | 8.642 ms | 1253× | **365×** |
| `mini-eval` | 0.0624 ms | 0.0936 ms | 19.247 ms | 308× | **206×** |
| `callcc` | 0.0840 ms | 0.0810 ms | 11.263 ms | 134× | **139×** |
| `strings` | 0.0709 ms | 0.0703 ms | 8.538 ms | 120× | **122×** |
| `closures` | 0.0049 ms | 0.1396 ms | 16.264 ms | 3319× | **117×** |
| `globals` | 0.0000 ms | 1.3988 ms | 64.679 ms | ∞ | **46×** |
| `vectors` | 0.0124 ms | 0.6884 ms | 29.344 ms | 2367× | **43×** |
| `locals` | 0.0000 ms | 1.3978 ms | 57.247 ms | ∞ | **41×** |
| `tail-loop` | 0.0490 ms | 1.3934 ms | 51.626 ms | 1054× | **37×** |
| `higher-order` | 0.3844 ms | 0.6550 ms | 18.831 ms | 49× | **29×** |
| `sort` | 0.3866 ms | 0.3971 ms | 10.679 ms | 28× | **27×** |
| `lists` | 1.9213 ms | 3.3401 ms | 71.148 ms | 37× | **21×** |

**Geometric mean: 66× slower than C doing the same work** (the twelve ratios
above, `globals` and `locals` counted at 46× and 41× since their `-O2` time is
zero), and 276× slower than `gcc -O2` is free to be.  The requested target was
10×; the closest single workload is `lists` at 21×.

## Why it is this far off

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

## What the numbers do not say

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

Even all five together would not reach 10× on `fib`.  Reaching 10× on programs
whose inner loop is arithmetic means unboxed values *and* inlined primitives
*and* no allocation per operation, which is a compiled-Scheme project rather
than a better interpreter.  That is worth saying plainly: the target was not
modest, and this document is the measurement that shows it.

## Files

* `bench/c/*.c` — the twelve C programs, each with a comment saying what it
  mirrors.
* `bench/scheme/*.scm` — the same twelve in Scheme, extracted from the panel so
  that both sides can be read side by side.
* `bench/time.scm` — the timer, written in GoScheme.
* `bench/run.sh` — builds both sides, runs them, prints the table.
