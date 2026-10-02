/* strings: append 3000 single-character strings, as the Scheme program does.
 * Each append copies, which is what makes this allocation-dominated in both
 * languages; C does it with realloc-and-copy under the same rule. */
#include "common.h"
#include <string.h>

typedef struct { char *bytes; size_t len; } Str;

static Str append_char(Str s) {
    Str out;
    out.len = s.len + 1;
    out.bytes = malloc(out.len + 1);
    memcpy(out.bytes, s.bytes, s.len);
    out.bytes[s.len] = 'x';
    out.bytes[out.len] = 0;
    return out;
}

static intptr_t run_strings(void) {
    Str acc = { strdup(""), 0 };
    for (intptr_t i = 0; i < 3000; i++) {
        Str next = append_char(acc);
        free(acc.bytes);
        acc = next;
    }
    intptr_t len = (intptr_t)acc.len;
    free(acc.bytes);
    return len;
}

int main(void) {
    double min = bench_min_seconds();
    intptr_t r = 0, reps = 0;
    double t0 = now_seconds(), elapsed = 0;
    do {
        r += run_strings();
        reps++;
        elapsed = now_seconds() - t0;
    } while (elapsed < min);
    report("strings", r, elapsed, reps);
    return 0;
}
