#!/bin/sh
# SPDX-License-Identifier: MIT
#
# Run the same twelve workloads in C and in GoScheme, and print the ratios.
#
#     bench/run.sh [runs]
#
# Two C columns, because they answer two different questions:
#
#   C (-O2)   the compiler may do anything, including replacing a loop with its
#             closed form.  This is "how fast is C", and for the arithmetic
#             loops the denominator is almost nothing.
#   C (kept)  the same program with the whole-loop rewrites held back, so C
#             performs the same sequence of steps an interpreter does.  This is
#             the column to compare an interpreter against.
#
# Timing is done by bench/time.scm rather than by /usr/bin/time, because the C
# programs take microseconds and the shell's clock has a 10 ms resolution.  The
# best of `runs` executions is kept for each program; the machine is shared, and
# the fastest run is the least disturbed.
set -e

RUNS=${1:-5}
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/.." && pwd)
CC=${CC:-cc}
CFLAGS=${CFLAGS:--O2 -std=c11}
# Held back for the "kept" column: no loop turned into a formula, no recursion
# turned into a loop, no tail call removed.
KEEP="-fno-optimize-sibling-calls -fno-inline-functions -fno-inline-small-functions"
KEEP="$KEEP -fno-aggressive-loop-optimizations -fno-tree-loop-optimize -fno-ipa-modref"
BUILD=${BUILD:-$ROOT/.build/bench-c}
GOSCHEME=${GOSCHEME:-$ROOT/.build/goscheme}

mkdir -p "$BUILD"

PROGRAMS="fib tail-loop closures globals locals lists vectors strings \
          higher-order mini-eval sort callcc"

timeit() { "$GOSCHEME" "$HERE/time.scm" "$RUNS" "$@"; }
ms() { awk -v s="$1" 'BEGIN { printf "%.4f", s * 1000 }'; }
ratio() { awk -v a="$1" -v b="$2" 'BEGIN { printf "%.1f", b / a }'; }

echo "C:      $CC $CFLAGS"
echo "C kept: $KEEP"
echo

printf '%-13s %10s %10s %11s %8s %8s\n' \
    program 'C -O2' 'C kept' GoScheme 'vs -O2' 'vs kept'
printf '%-13s %10s %10s %11s %8s %8s\n' \
    ------- ---------- ---------- ----------- -------- --------

log_native=0
log_kept=0
n=0
for p in $PROGRAMS; do
    src=$(echo "$p" | tr '-' '_')
    $CC $CFLAGS -o "$BUILD/$p" "$HERE/c/$src.c" -lm
    $CC $CFLAGS $KEEP -o "$BUILD/$p.kept" "$HERE/c/$src.c" -lm
    # .scmc, not .scm: the file is bytecode, and the interpreter decides how to
    # read a file by its extension.  Naming it .scm made it parse the bytecode
    # as source, which is a program that does nothing — and that is what the
    # first version of this script measured.
    "$GOSCHEME" compile "$HERE/scheme/$p.scm" -o "$BUILD/$p.scmc"

    a=$(timeit "$BUILD/$p")
    b=$(timeit "$BUILD/$p.kept")
    c=$(timeit "$GOSCHEME" "$BUILD/$p.scmc")

    printf '%-13s %9sms %9sms %10sms %7sx %7sx\n' "$p" \
        "$(ms "$a")" "$(ms "$b")" "$(ms "$c")" "$(ratio "$a" "$c")" "$(ratio "$b" "$c")"

    log_native=$(awk -v t="$log_native" -v a="$a" -v c="$c" 'BEGIN { print t + log(c / a) }')
    log_kept=$(awk -v t="$log_kept" -v b="$b" -v c="$c" 'BEGIN { print t + log(c / b) }')
    n=$((n + 1))
done

echo
echo "geometric mean: $(awk -v t="$log_native" -v n="$n" 'BEGIN { printf "%.1f", exp(t / n) }')x vs -O2," \
     "$(awk -v t="$log_kept" -v n="$n" 'BEGIN { printf "%.1f", exp(t / n) }')x vs the kept build"
