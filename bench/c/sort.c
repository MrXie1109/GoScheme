/* sort: merge sort of a 1000-element list, built the same way as the Scheme
 * one (i * 7919 mod 2000, consed in reverse). */
#include "common.h"

static Pair *merge(Pair *a, Pair *b) {
    if (a == NULL) return b;
    if (b == NULL) return a;
    if (b->car < a->car) return cons(b->car, merge(a, b->cdr));
    return cons(a->car, merge(a->cdr, b));
}

/* split returns the two halves through out parameters: C has no values(). */
static void split(Pair *l, Pair **first, Pair **second) {
    Pair *slow = l, *fast = l, *acc = NULL;
    while (!(fast == NULL || fast->cdr == NULL)) {
        acc = cons(slow->car, acc);
        slow = slow->cdr;
        fast = fast->cdr->cdr;
    }
    /* reverse acc */
    Pair *rev = NULL;
    for (Pair *p = acc; p; p = p->cdr) rev = cons(p->car, rev);
    *first = rev;
    *second = slow;
}

static Pair *msort(Pair *l) {
    if (l == NULL || l->cdr == NULL) return l;
    Pair *a, *b;
    split(l, &a, &b);
    return merge(msort(a), msort(b));
}

static Pair *build(intptr_t i, Pair *acc) {
    return i == 0 ? acc : build(i - 1, cons((i * 7919) % 2000, acc));
}

int main(void) {
    double min = bench_min_seconds();
    intptr_t r = 0, reps = 0;
    double t0 = now_seconds(), elapsed = 0;
    do {
        r += msort(build(1000, NULL))->car;
        reps++;
        elapsed = now_seconds() - t0;
    } while (elapsed < min);
    report("sort", r, elapsed, reps);
    return 0;
}
