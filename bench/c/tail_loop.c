/* tail-loop: 200000 iterations of a tail-recursive accumulator. */
#include "common.h"

static intptr_t loop(intptr_t i, intptr_t acc) {
    return i == 0 ? acc : loop(i - 1, acc + i);
}

int main(void) {
    intptr_t n = loop_count("BENCH_N", 200000);
    double min = bench_min_seconds();
    intptr_t r = 0, reps = 0;
    double t0 = now_seconds(), elapsed = 0;
    do {
        r = loop(n, r);
        reps++;
        elapsed = now_seconds() - t0;
    } while (elapsed < min);
    report("tail-loop", r, elapsed, reps);
    return 0;
}
