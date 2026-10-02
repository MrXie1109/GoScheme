# SPDX-License-Identifier: MIT
#
# What the Python benchmarks need, and nothing more.
#
# These are the same twelve workloads as bench/c and bench/scheme, written the
# way a Python programmer would write them: lists are Python lists where the
# Scheme program builds a list, a closure is a def inside a def, and integers
# are Python ints.  The point of including Python is that it is the closest
# thing to a fair fight here — it boxes everything, it dispatches dynamically,
# it has a garbage collector — and it is still a compiled bytecode machine with
# decades of optimisation behind it, not a tree-walker written in a week.
#
# Every program repeats until the clock has moved and reports the time per run,
# for the same reason the C ones do: one execution is microseconds, and timing
# that from outside the process measures the interpreter's startup.
import time

MIN_SECONDS = 0.25


def repeat(run, name, *args):
    """Run `run` until MIN_SECONDS have passed; report the mean per run."""
    reps = 0
    total = 0
    t0 = time.perf_counter()
    elapsed = 0.0
    while elapsed < MIN_SECONDS:
        total += run(*args)
        reps += 1
        elapsed = time.perf_counter() - t0
    print(f"{name:<14} {total:>14}   {elapsed / reps * 1e3:9.4f} ms/run   {reps:5d} runs")
    return total
