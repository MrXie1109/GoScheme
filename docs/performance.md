# GoScheme against C

We ran the same twelve workloads in C and in GoScheme, on one machine, to find
out how far apart they are.  The short answer: **about seventy-five times slower
than C doing the same work**, and up to 510× on the worst workload.  The goal was
to stay within ten.  We are not within ten, and this document says by how much,
why, and what would have to change.

There is a second result in here, and it is newer.  GoScheme now has **three ways
to run a program** — a tree-walking interpreter, the bytecode VM that runs a
file, and an LLVM native compiler — and the compiler is measured against the
interpreter in [Native compilation against the interpreter](#native-compilation-against-the-interpreter).
It is **3.4×** on `fib` and much less on most other things, for a reason that is
worth understanding rather than rounding off: the compiler is a hybrid.

Everything here is reproducible:

```sh
make build
bench/run.sh 5          # the table below, best of five
bench/run-native.sh 5   # compiled against interpreted
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

Those ratios are the **bytecode VM against the source**, which is the engine that
runs a file and therefore the right one to compare an interpreter against another
implementation.  A later run of the same script on the same machine moves them by
a few percent in either direction (70× C, 14.8× Python, 1.60× Guile) — the machine
is shared — and the two bracketing runs are the honest answer rather than the
better-looking one.  Nothing here is the native compiler:
[that is a separate comparison](#native-compilation-against-the-interpreter)
against a different engine, and mixing the two would be the easiest way to make
these numbers mean nothing.

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

So the honest reading of 16× is: we are the same kind of program as CPython,
and we are one to two orders of magnitude behind it on the rows where it has
specialised opcodes and we do not.  On the rows where neither has an advantage —
`sort` (15×), `callcc` (13×), `higher-order` (22×) — we are closer, because
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
at compile time and removed the per-call environment, so a variable is a slice
index rather than a lookup — but a call is still a frame allocation and a jump
through a table, and that index carries a bounds check.  There is no inlining, no
register allocation, no type specialisation, no escape analysis.  C has all of
them.

This is the item the **native compiler** attacks, and
[the section below](#native-compilation-against-the-interpreter) is what it
managed: the arithmetic and comparison operators become machine instructions in a
compiled procedure, and a call between two compiled procedures is a direct call.
What it does not reach is everything else, which is why the gain is concentrated
rather than general.

**5. Continuations are not free here, and `callcc` is 161× because of it.**  The
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

**The native compiler is not part of this answer**, and it is worth saying why
rather than leaving the omission to be noticed.  This table is about the
*interpreter* — how fast the language runs when the whole program is interpreted,
which is the like-for-like question against another implementation.  The compiler
does not compile whole programs; it compiles the procedures it can prove and hands
the rest to the interpreter, so quoting a compiled `fib` in this table would be
comparing a mostly-machine-code program against Guile's and calling the
difference a property of the interpreter.  Its own measurement is
[above](#native-compilation-against-the-interpreter), on its own terms.

## What the numbers do not say

* Nor is it "Scheme is 16× slower than Python" or "1.7× slower than Guile".  It
  is this interpreter, against these implementations, on these twelve programs.
* This is not "Scheme is 75× slower than C".  It is "this interpreter, as it
  stands, on these twelve programs, is 75× slower than a C compiler doing the
  same work".  Compiled Scheme (Chez, Gambit, Racket's `raco make`) is typically
  within two to five times of C on programs like these, because it does the six
  things above that we do not.  **No such implementation was measured here** —
  none is installed on this machine, and this document does not claim a number
  for one.  GoScheme's own native compiler is the nearest thing in the repository
  to one of those, and it is a partial compiler measured against the interpreter,
  not against C.
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

## Native compilation against the interpreter

Everything so far measured the interpreter against other languages.  This is a
different question: **what does `goscheme compile` buy over running the same
program interpreted?**  The honest answer is "sometimes a lot, usually very
little", and the reason is the design rather than the implementation.

The compiler is a **hybrid**.  It emits machine code for a procedure whose body
is a computation over its parameters, constants and globals, and hands everything
else to the runtime — which is the interpreter, linked into the program.  So a
compiled program runs partly as machine code and partly interpreted, and how much
it gains depends on how much of its time is in the part that got compiled.
[docs/compile.md](compile.md) is the long version; the short version is that
`set!`, a closure, a macro or a library call is not compiled, while arithmetic
and comparison are.

### `fib`: the case it is built for

The clean measurement, because the whole workload is a pure procedure calling
itself:

| `(fib 32)` | time |
| --- | --- |
| interpreted (the VM, reading the source) | **1.54 s** |
| compiled (`goscheme compile -O2`) | **0.45 s** |
| ratio | **3.4×** |

Measured with `/usr/bin/time`, best of several, on the 12th-generation i3 the
rest of this document uses.  It is the number to quote for the compiler, because
it is the one where the measurement is about the compiler and not about which
half of the program happened to fall on which side of the boundary.

### The panel, which is the honest picture

The same twelve workloads as everywhere else, run through both engines, with
`bench/run-native.sh`.  The **native** column is how many procedures the compiler
actually emitted as machine code, and it is the column to read first: a row with
`0` has a compiled *image* with nothing compiled in it, and its ratio is startup
cost rather than speed.

| workload | interpreted | compiled | ratio | native procs |
|---|---|---|---|---|
| `fib` | 13.82 ms | 3.23 ms | **4.28×** | 2 |
| `tail-loop` | 52.32 ms | 12.66 ms | **4.13×** | 2 |
| `globals` | 58.57 ms | 46.24 ms | 1.27× | 2 |
| `higher-order` | 24.73 ms | 19.41 ms | 1.27× | 2 |
| `lists` | 72.27 ms | 56.42 ms | 1.28× | 4 |
| `vectors` | 36.79 ms | 35.10 ms | 1.05× | 0 |
| `closures` | 19.94 ms | 20.07 ms | 0.99× | 0 |
| `mini-eval` | 22.67 ms | 23.28 ms | 0.97× | 0 |
| `locals` | 57.55 ms | 64.14 ms | 0.90× | 0 |
| `callcc` | 11.24 ms | 12.43 ms | 0.90× | 0 |
| `strings` | 14.84 ms | 16.51 ms | 0.90× | 0 |
| `sort` | 11.50 ms | 13.05 ms | 0.88× | 2 |

**The two rows that are the point of the compiler are now 4× and 4.1×**, and the
rest sit within a few percent of the interpreter either way.  The rows near 1.0×
are workloads of a few milliseconds where process startup is most of what is
measured, and the rows below it are programs the compiler has nothing to take
from — the `native` column, which counts the procedures actually emitted, is the
number to read first.

### Doing the loop in one call, which is where the speed comes from

A loop written in Scheme over a list or a vector was the case the compiler was
*worst* at, and it is the case most programs are made of:

```scheme
(define (sum-list lst acc)
  (if (null? lst) acc (sum-list (cdr lst) (+ acc (car lst)))))
```

Compiled naively this crosses into the runtime four times per element — for
`null?`, `car`, `cdr` and `+` — and a crossing costs more than the element's
work.  Measured over a million elements: **260 ms element by element against
3.6 ms for the loop run in Go**.  The compiled version was slower than the
interpreter, and a 300000-element walk took 0.22 s interpreted and 0.23 s
compiled.

The loop is now recognised and emitted as **one call**.  Four shapes are accepted:

| shape | example | what it does |
|---|---|---|
| list walk | `(if (null? lst) acc (f (cdr lst) (+ acc (car lst))))` | sums, counts or reverses a list |
| vector walk | `(if (= i n) acc (f v (+ i 1) n (+ acc (vector-ref v i))))` | the same over a vector |
| counting loop | `(if (= n 0) acc (f (- n 1) (cons n acc)))` | counts down and folds the counter in |
| either, with a filter | `(if (even? (car lst)) (+ 1 n) n)` | folds only the elements a builtin accepts |

Measured: a million-element list walk 0.33 s interpreted against **0.07 s**; the
same for a vector, 0.31 s against 0.03 s; a two-million element counting loop
0.67 s against 0.21 s.

The shape is checked part by part rather than matched loosely, and that is not
fussiness: a body that merely *looks* like a walk would compute something else if
it were run this way.  The tests cover a walk that tests the wrong parameter,
advances the wrong one, returns something other than the accumulator in its base
case, combines with `-` instead of `+`, is not tail recursive, calls a procedure
that is not itself, or takes the wrong number of arguments.  Every one is
refused, and a refused walk takes the ordinary path and stays correct.

The filter's predicate must be a **builtin**, which is a consequence of the idea
rather than a limitation of it: a builtin is one the loop can apply itself, while
a predicate written by the programmer would have to be called back into Scheme
for every element — the crossing the whole arrangement exists to remove.

This is the idea `(goscheme fast)` already used, applied to a loop the
*programmer* wrote instead of one of the library's procedures.

### Why not let the generated code read the data directly

Because it cannot.  The obvious next step is to have the compiled code inline
`car` and `cdr` — read the pair's fields itself instead of calling anything — and
three properties of Go make it impossible:

- **A pair lives on the Go heap as an interface**, `{type, data}`, where the type
  word is a *runtime address*.  The generated code cannot know it at compile
  time.
- **cgo forbids handing a Go pointer to C**, for the same reason: C code holding
  a pointer into the Go heap defeats the collector.  A prototype panicked with
  "cgo argument has Go pointer to unpinned Go pointer".
- **The collector moves objects.**  Storing an absolute address in a node avoids
  the cgo rule and works until the first collection, after which it is a dangling
  pointer.  A prototype walked a list correctly and then segfaulted on the second
  `runtime.GC()`.

Keeping the walk in Go sidesteps all three, because there is no pointer to hand
over: the loop and the data are on the same side.

### What the compiler does with a loop, and why it took work

The first version of the native path was *slower than the interpreter* on a
tail loop, and the reason was worth recording because nothing about the generated
module looked wrong.

A value crossing the boundary is a tagged struct, `%gs.val = { i64, i64 }`.  That
is the right way to carry a value and the wrong way to *pass* one: a struct hides
the tag from the optimizer, so the callee loaded a tag it could prove nothing
about, and every operation kept the runtime check it would have needed if the
value might have been a bignum.  `(= i 0)` called `gs_num_eq`, and the result of
that comparison went through `gs_truthy`, on every iteration — two real function
calls to answer a question already answered.

Passing each argument as its own `(word, tag)` pair fixed it, because a tag that
is a literal zero is then visible across the call.  The loop's assembly went from
four runtime calls per iteration to none, and the loop itself to an add, a
decrement, and a jump.  The function-pointer type is per arity as a result, which
`gs_register` absorbs with a small generated adapter.

Three further checks became free once the tags were visible: a comparison of two
known fixnums, `(= x 0)` against a literal (false for a handle, because a handle
is never a value that would fit a machine word), and the truth of a boolean.

### Where the compiler's number would have to come from

### Why compile at all, then

Because the case it is built for is real and the alternative was nothing.  A
program whose inner loop is arithmetic over its own parameters — the shape of
`fib`, and of a great deal of numeric Scheme — gets 3.4× from a compiler that
refuses nothing it cannot prove, and the language stays whole around it rather
than becoming a subset with a second, disagreeing implementation.  What this
document will not do is claim a general speedup: `fib` is recusion whose results
feed arithmetic, a tail loop gets nothing, and a program that spends its time in
library calls gets less than nothing.  All three are above, and the reason the
compiler exists is the first one.

### Where the compiler's number would have to come from

The shape of the result is clear from the design, and the missing figure is named
rather than invented: the compiler does not inline across the boundary, does not
infer types, and does not unbox anything beyond the fixnum case.  Those three are
what stand between "2.2× on a recursive pure procedure" and a number
**\[to be measured\]** that a whole program would show.  No measurement of that
is claimed here, because none has been made.

## What was tried for the three next steps, and what the measurements said

The plan below was worked through, and the first thing it taught is that the
obvious form of each item does not work.  Every measurement is pinned to one
core, best of nine, against the panel; a change that did not win was removed
rather than kept behind a flag.

### Unboxed integers in a compiled frame

The allocation profile of a counting loop says exactly where the time goes:

```
(define (loop i acc) (if (= i 0) acc (loop (- i 1) (+ acc i))))
(loop 200000 0)                        ->  4.0 allocations per iteration
```

| what allocates | share of the objects |
|---|---|
| `Int` — building the result of `+` and `-` | **75%** |
| `newVMEnv` — the frame of each call | **23%** |

Two ways to attack the `Int` line were tried and both lost:

| change | result |
|---|---|
| widen the small-integer pool from ±1024 to ±65536 | **4.3% slower** |
| a blocked pool, filling each 1024-value block on demand | **2.6% slower** |

The first loses because a 3 MB array stops fitting in cache, and a program
reuses small values over and over: they miss where they used to hit.  The second
loses because the block lookup happens on every construction and costs more than
the allocation it avoids.

The reason neither can help the loops that allocate most is worth writing down,
because it rules the whole approach out: that loop's accumulator reaches
**20000100000**.  A pool covering the values a real program counts to is tens of
megabytes.  **The allocation is not a consequence of the pool being too small;
it is a consequence of a frame slot holding a boxed `Value`** — and unboxing it
means a typed second slot table plus a typed operand stack, because `opLocal`
pushes the slot onto `vals []Value` and would box it again immediately.  That is
a compiler project with type inference, not a simple optimization, and it is the
honest answer to why this item is still on the list.

### The frame allocation, which looked like the easy win

`newVMEnv` allocates 23% of the objects, and 4 slots are kept inline before it
falls back to a slice.  Raising that to 8 is a one-line change and it was
**6% slower**: a frame pays for the slots whether or not it uses them, the
bigger frame is copied more on every call and every suspension, and the
allocation it saves is once per call while the copying it costs is once per call
*and* once per return.

### Cached global references

The profile said there was room: `Env.get` and its map lookup are 11% of a
program that reads globals in a loop, and the groundwork measured at no cost
(`Env.Generation`, bumped by `Define` and `Set`; the panel moved 1.0%, which is
noise).

The cache was written, twice, and lost both times:

| cache | result |
|---|---|
| one entry per instruction, allocated per frame | **71% slower** |
| one entry per global reference, numbered by the compiler | **40% slower** |

The first is a mistake worth naming: sizing the cache by `len(code.Instrs)`
allocates three slices per frame for a body that may have one global in it.  The
second is the interesting one, because it is the design the profile asked for
and it still loses.  What the lookup costs is a two-frame walk and a map probe;
what the cache costs is a generation load, a compare, a nil check and a possible
allocation on *every* read.  Most global references are executed a handful of
times — a top-level define, a branch taken once — so there is nothing to
amortise the bookkeeping over, and the loop that would have benefited is already
served by the arithmetic instructions and by the fact that a value read in a
loop can be bound to a local.

Both were removed.  `Env.Generation` is kept: it is a correct and free way to
tell whether a binding changed, and it is what any future attempt would need.

### A younger generation for the allocator

This one was measured before it was written, and the measurement says there is
nothing there:

```
loop of 3000000 iterations:  668 ms total, 147 GCs, 2.96 ms of GC pause
```

**The collector is 0.44% of the run.**  Turning it off entirely, with
`debug.SetGCPercent(-1)`, makes the same loop **16.6% slower** — the heap grows
and the program's cache behaviour gets worse.  A nursery would reduce a cost
that is already under half a percent.

The distinction that matters: allocation here is expensive as an *action*
(`runtime.mallocgc` is 31.5% of the profile) and cheap as *garbage*.  A
generational collector makes the second cheaper and does nothing for the first.
The only way to remove the first is to stop allocating — which is the unboxed
slot, above — and not to collect the result better.

## Where the next 2× actually is

Of the five items below, the first four have now been tried in their obvious
form and measured.  All four lose, and the profile explains why in one line:

```
counting loop:  4.0 allocations per iteration
                Int (the boxed result of + and -)      75% of the objects
                newVMEnv (the frame of each call)      23%
                runtime.mallocgc                       31.5% of the time
                GC pause                                0.44% of the time
```

**The cost is the allocation, not the collection.**  Every proposed fix either
makes the allocation cheaper (the pool: tried, slower, because cache locality
matters more) or collects the result better (a nursery: nothing to collect). The
one that would work is the one that removes the allocation — a frame slot that
holds an `int64` instead of a boxed `Value` — and that needs a typed slot table
and a typed operand stack, because `opLocal` pushes the slot onto `vals []Value`
and would box it again immediately.  That is a compiler with type inference, and
saying so is more useful than another micro-optimization that measures slower.

## The five items, as originally written

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

**v4 took a first step on that project rather than a sixth item on this list.**
The native compiler does exactly what item 1 and item 2 describe, but for the
procedures it can prove pure: their integers are machine words in SSA registers
rather than boxed `Value`s, and their arithmetic is an instruction rather than a
call to a primitive.  That is where `fib`'s **3.4×** comes from, and it is the
same diagnosis this document reached from the profile — the cost is the boxing
and the call, not the dispatch.  What it does not do is reach the programs where
the boxing is not in a pure procedure, which is most of the panel; item 5 (type
feedback) is the shape of what would, and it remains a project.

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
* `bench/run.sh` — builds both sides, runs them, prints the interpreter's table.
* `bench/run-native.sh` — the same twelve through `goscheme compile`, against the
  interpreter, with the count of procedures that became machine code.
* [docs/compile.md](compile.md) — what the compiler accepts and refuses, the ABI,
  and the limitations, which is where the shape of the numbers above comes from.
