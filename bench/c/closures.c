/* closures: 20000 closures created and called once each.  A closure is a
 * function pointer and an environment; each one is malloc'd, as an interpreter
 * would have to. */
#include "common.h"

typedef struct { intptr_t (*fn)(void *env, intptr_t); void *env; } Closure;

static intptr_t add_n(void *env, intptr_t x) { return x + *(intptr_t *)env; }

static Closure *make(intptr_t n) {
    Closure *c = malloc(sizeof(Closure));
    intptr_t *cell = malloc(sizeof(intptr_t));
    *cell = n;
    c->fn = add_n;
    c->env = cell;
    return c;
}

static intptr_t run(intptr_t i, intptr_t acc) {
    if (i == 0) return acc;
    Closure *c = make(i);
    intptr_t v = c->fn(c->env, 1);
    free(c->env);
    free(c);
    return run(i - 1, acc + v);
}

int main(void) {
    intptr_t n = loop_count("BENCH_N", 20000);
    double min = bench_min_seconds();
    intptr_t r = 0, reps = 0;
    double t0 = now_seconds(), elapsed = 0;
    do {
        r += run(n, 0);
        reps++;
        elapsed = now_seconds() - t0;
    } while (elapsed < min);
    report("closures", r, elapsed, reps);
    return 0;
}
