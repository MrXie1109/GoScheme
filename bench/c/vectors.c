/* vectors: fill a 1000-element array and sum it 100 times. */
#include "common.h"

static intptr_t v[1000];

static void fill(intptr_t i) {
    if (i == 1000) return;
    v[i] = i;
    fill(i + 1);
}

static intptr_t sum(intptr_t i, intptr_t acc) {
    return i == 1000 ? acc : sum(i + 1, acc + v[i]);
}

static intptr_t run_vectors(void) {
    fill(0);
    intptr_t total = 0;
    for (intptr_t n = 0; n < 100; n++) total += sum(0, 0);
    return total;
}

int main(void) {
    double min = bench_min_seconds();
    intptr_t r = 0, reps = 0;
    double t0 = now_seconds(), elapsed = 0;
    do {
        r += run_vectors();
        reps++;
        elapsed = now_seconds() - t0;
    } while (elapsed < min);
    report("vectors", r, elapsed, reps);
    return 0;
}
