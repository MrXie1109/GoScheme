/* SPDX-License-Identifier: MIT
 *
 * What the Scheme benchmarks need from C, and nothing more.
 *
 * These programs are deliberately written the way a C programmer would write
 * them for the same job, with the same data structures: a list is a cell and a
 * pointer, a vector is an array, a closure is a function plus an environment
 * the caller passes.  The point is not to handicap C — it is to measure the
 * distance between "what a Scheme program costs on our interpreter" and "what
 * the same algorithm costs when a compiler has the whole picture", which is the
 * honest upper bound on what an interpreter can hope for.
 *
 * One thing is deliberately not left to the optimiser: the timer wraps only the
 * computation, and the result is printed through a volatile sink, so a
 * benchmark whose answer the compiler could predict still runs.  Without that,
 * gcc -O2 folds `tail-loop` into a constant and the comparison is between an
 * interpreter and arithmetic on paper.
 */
#ifndef BENCH_COMMON_H
#define BENCH_COMMON_H

/* clock_gettime and CLOCK_MONOTONIC are POSIX: with -std=c11 they are hidden
 * unless this is asked for, and the compiler then guesses a prototype, which
 * is a warning at best and a wrong call at worst. */
#define _POSIX_C_SOURCE 200809L

#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <time.h>

static double now_seconds(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (double)ts.tv_sec + (double)ts.tv_nsec / 1e9;
}

/* A Scheme pair.  malloc per cell, as an interpreter would: a bump allocator
 * would be a different program, and this is meant to be the same one. */
typedef struct Pair {
    intptr_t car;
    struct Pair *cdr;
} Pair;

static Pair *cons(intptr_t car, Pair *cdr) {
    Pair *p = malloc(sizeof(Pair));
    p->car = car;
    p->cdr = cdr;
    return p;
}

/* The sink keeps the compiler honest: the result is read here as well as
 * printed, so the computation cannot be removed for being unused. */
static volatile intptr_t sink;

/* For the "same work, optimiser held back" column the runner compiles with
 * -fno-inline-small-functions -fno-optimize-sibling-calls and friends; this
 * marker lets each program also run its plain -O2 build, where the optimiser is
 * free to fold.  See bench/README.md. */

/* loop_count is a trip count the optimiser cannot see.  A loop whose bound is
 * a literal gets replaced by the closed form — summing 1..200000 is arithmetic,
 * not a loop — which measures nothing at all.  Reading the count from the
 * environment makes the loop opaque at compile time while leaving the work an
 * interpreter actually does (one iteration at a time) intact.  The benchmarks
 * pass the same numbers the Scheme programs use; -O2 then cannot fold them. */
static intptr_t loop_count(const char *name, intptr_t fallback) {
    const char *env = getenv(name);
    if (env && *env) return strtoll(env, NULL, 10);
    return fallback;
}

/* A run is too short to time from outside the process: the process itself
 * starts in half a millisecond, and a microsecond of arithmetic disappears in
 * that noise.  So the whole benchmark is repeated until it has taken at least
 * BENCH_MIN_SECONDS (default 0.25), and the *per-iteration* time is what gets
 * reported.  The repetition count is printed too, so the reader can see how
 * much work stands behind the number.
 *
 * Repetition also gives the optimiser more to chew on if it wants to, which is
 * why the kept build exists: it repeats the same work without the rewrites. */
static void report(const char *name, intptr_t value, double seconds, intptr_t reps) {
    sink = value;
    printf("%-14s %14lld   %9.4f ms/run   %5lld runs\n",
           name, (long long)value, seconds * 1e3 / (double)reps, (long long)reps);
}

/* The next trip count for the repetition loop: each run's result feeds the
 * next run's input, so no run can be hoisted out of the loop or replaced by a
 * previous answer.  A benchmark that ignores this measures the optimiser. */
static intptr_t vary(intptr_t n, intptr_t acc) {
    intptr_t next = n + (acc & 1);
    return next;
}

static double bench_min_seconds(void) {
    const char *env = getenv("BENCH_MIN");
    if (env && *env) return strtod(env, NULL);
    return 0.25;
}

#endif
