/* locals: the same shape with two locals captured by an inner loop. */
#include "common.h"

static intptr_t inner(intptr_t j, intptr_t acc, intptr_t a, intptr_t b) {
    return j == 0 ? acc : inner(j - 1, acc + a + b, a, b);
}

static intptr_t loop(intptr_t i) {
    intptr_t a = 1, b = 2;
    return inner(i, 0, a, b);
}

int main(void) {
    intptr_t n = loop_count("BENCH_N", 200000);
    double min = bench_min_seconds();
    intptr_t r = 0, reps = 0;
    double t0 = now_seconds(), elapsed = 0;
    do {
        r = loop(n + (r & 1));
        reps++;
        elapsed = now_seconds() - t0;
    } while (elapsed < min);
    report("locals", r, elapsed, reps);
    return 0;
}
