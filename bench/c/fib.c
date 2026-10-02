/* fib: non-tail recursion with arithmetic, the same n as the Scheme panel. */
#include "common.h"

static intptr_t fib(intptr_t n) {
    return n < 2 ? n : fib(n - 1) + fib(n - 2);
}

int main(void) {
    intptr_t n = loop_count("BENCH_N", 20);
    double min = bench_min_seconds();
    intptr_t r = 0, reps = 0;
    double t0 = now_seconds(), elapsed = 0;
    do {
        r += fib(n);
        reps++;
        elapsed = now_seconds() - t0;
    } while (elapsed < min);
    report("fib", r, elapsed, reps);
    return 0;
}
