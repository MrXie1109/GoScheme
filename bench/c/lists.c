/* lists: build 100000 cells, then walk them summing the cars. */
#include "common.h"

static Pair *build(intptr_t i, Pair *acc) {
    return i == 0 ? acc : build(i - 1, cons(i, acc));
}

static intptr_t sum(Pair *l, intptr_t acc) {
    return l == NULL ? acc : sum(l->cdr, acc + l->car);
}

int main(void) {
    double min = bench_min_seconds();
    intptr_t r = 0, reps = 0;
    double t0 = now_seconds(), elapsed = 0;
    do {
        r += sum(build(100000, NULL), 0);
        reps++;
        elapsed = now_seconds() - t0;
    } while (elapsed < min);
    report("lists", r, elapsed, reps);
    return 0;
}
