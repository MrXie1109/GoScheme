# bench

The C comparison.  Twelve workloads, each written twice: once in C
(`bench/c/`) and once in Scheme (`bench/scheme/`, extracted from the panel in
`internal/scheme/vm_panel_test.go` so that the two can be read side by side).

```sh
make build
bench/run.sh 5        # best of five, prints the table
```

`bench/time.scm` is the timer, and it is itself a GoScheme program: the shell's
clock has a 10 ms resolution and most of these programs finish sooner than that,
so timing has to come from inside something with a nanosecond clock.

Read [docs/performance-vs-c.md](../docs/performance-vs-c.md) for the numbers and
what they mean.  The short version: **66× slower than C doing the same work**,
21× at best, 365× at worst, against a target of 10×.

Two things to know before changing anything here:

* **C at `-O2` does not run these loops; it solves them.**  Summing 1..200000 is
  a multiplication, so `globals.c` takes 0.0000 ms and the ratio is infinite.
  The "kept" column holds those rewrites back for that reason, and it is the one
  the summary uses.
* **Every program repeats until the clock has moved** and reports time per run.
  Timing one execution from outside the process measures process startup — 0.5 ms
  for C, 2.3 ms for GoScheme — which is more than most of the work.
