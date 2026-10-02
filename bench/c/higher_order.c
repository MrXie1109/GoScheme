/* higher-order: a fold over a 20000-element list, with a closure as f. */
#include "common.h"

typedef intptr_t (*BinOp)(intptr_t, intptr_t);

static intptr_t add(intptr_t a, intptr_t b) { return a + b; }

static intptr_t fold(BinOp f, intptr_t init, Pair *l) {
    return l == NULL ? init : fold(f, f(init, l->car), l->cdr);
}

static Pair *build(intptr_t i, Pair *acc) {
    return i == 0 ? acc : build(i - 1, cons(i, acc));
}

int main(void) {
    intptr_t n = loop_count("BENCH_N", 20000);
    double min = bench_min_seconds();
    intptr_t r = 0, reps = 0;
    double t0 = now_seconds(), elapsed = 0;
    do {
        r += fold(add, 0, build(n, NULL));
        reps++;
        elapsed = now_seconds() - t0;
    } while (elapsed < min);
    report("higher-order", r, elapsed, reps);
    return 0;
}
