/* callcc: the Scheme program captures a continuation inside a procedure and
 * re-enters it 20000 times; each re-entry runs from the capture point to the
 * test again, and i counts the entries.
 *
 * A continuation in C is a saved point in the control flow, and setjmp/longjmp
 * is the only one the language has.  The trap is that a longjmp back to a
 * setjmp restores the *machine state* but not the local variables' values
 * unless they are volatile — and i is exactly the variable that has to survive,
 * so it is volatile here.  Getting that wrong makes the loop run for ever,
 * which is what the first version of this file did. */
#include "common.h"
#include <setjmp.h>

static jmp_buf k;
static volatile intptr_t v;
static volatile intptr_t i;

static intptr_t count_to(intptr_t n) {
    i = 0;
    v = 0;
    if (setjmp(k) == 0) {
        /* the body of the call/cc: it captures k and returns 0 */
    } else {
        /* a re-entry: the continuation's value is v, already incremented */
    }
    i++;
    if (i < n) {
        v++;
        longjmp(k, 1);
    }
    return v;
}

int main(void) {
    intptr_t n = loop_count("BENCH_N", 20000);
    double min = bench_min_seconds();
    intptr_t r = 0, reps = 0;
    double t0 = now_seconds(), elapsed = 0;
    do {
        r += count_to(n);
        reps++;
        elapsed = now_seconds() - t0;
    } while (elapsed < min);
    report("callcc", r, elapsed, reps);
    return 0;
}
