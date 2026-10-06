# bench

The comparison.  Twelve workloads, each written three times: in C
(`bench/c/`), in Python (`bench/python/`) and in Scheme (`bench/scheme/`,
extracted from the panel in `internal/scheme/vm_panel_test.go` so that the three
can be read side by side).  All three compute the same answers, which is
checked.

```sh
make build
bench/run.sh 5        # best of five, prints the table
```

`bench/run-native.sh` is the second question, and a separate script rather than
another column: it times the **native compiler** against the interpreter, on the
same twelve programs.

```sh
bench/run-native.sh 5 # compiled against interpreted, with the native count
```

The compiler is a hybrid — it emits machine code for a procedure whose body is a
computation over its parameters, constants and globals, and hands everything else
to the runtime — so the answer varies wildly by program, and that script prints
the number of procedures it compiled beside each ratio.  A row reading `0` there
is a compiled *image* with nothing compiled in it, and its ratio is startup cost
rather than speed.

`bench/time.scm` is the timer, and it is itself a GoScheme program: the shell's
clock has a 10 ms resolution and most of these programs finish sooner than that,
so timing has to come from inside something with a nanosecond clock.

Read [docs/performance.md](../docs/performance.md) for the numbers and
what they mean.  The short version: **75× slower than C doing the same work**,
and **16× slower than CPython 3.12**, with eight of the twelve rows within ten
of Python; against Guile, a compiled Scheme, **1.68×**.  The compiled path is
**3.4×** the interpreter on `fib` and much less on programs the hybrid cannot
take.  The original target was 10× against C, and that is not met.

Two things to know before changing anything here:

* **C at `-O2` does not run these loops; it solves them.**  Summing 1..200000 is
  a multiplication, so `globals.c` takes 0.0000 ms and the ratio is infinite.
  The "kept" column holds those rewrites back for that reason, and it is the one
  the summary uses.
* **Every program repeats until the clock has moved** and reports time per run.
  Timing one execution from outside the process measures process startup — 0.5 ms
  for C, 2.3 ms for GoScheme — which is more than most of the work.
