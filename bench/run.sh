#!/bin/sh
# SPDX-License-Identifier: MIT
#
# Run the same twelve workloads in C, Python and GoScheme, and print the ratios.
#
#     bench/run.sh [runs]
#
# The C side has two columns, because C answers two different questions:
#
#   C -O2     the compiler may do anything, including replacing a loop with its
#             closed form.  For globals and locals it removes the loop entirely
#             and the denominator is zero.
#   C kept    the same program with the whole-loop rewrites held back, so C
#             performs the same sequence of steps an interpreter does.  This is
#             the column the summary uses.
#
# Python is the fairer opponent: it boxes everything, dispatches dynamically and
# has a garbage collector, and it is still a compiled bytecode machine.  It has
# no tail calls, so tail-loop is a while loop there and recursion in Scheme —
# the one row where the three programs are not the same shape.
#
# Each program repeats until the clock has moved and reports time per run; a
# single execution is microseconds, and timing one from outside the process
# measures process startup (0.5 ms for C, 2.3 ms for GoScheme, 15 ms for
# CPython).  The Scheme side is timed by bench/time.scm, which is itself a
# GoScheme program, because /usr/bin/time has a 10 ms resolution.
set -e

RUNS=${1:-5}
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/.." && pwd)
CC=${CC:-cc}
CFLAGS=${CFLAGS:--O2 -std=c11}
KEEP="-fno-optimize-sibling-calls -fno-inline-functions -fno-inline-small-functions"
KEEP="$KEEP -fno-aggressive-loop-optimizations -fno-tree-loop-optimize -fno-ipa-modref"
BUILD=${BUILD:-$ROOT/.build/bench-c}
GOSCHEME=${GOSCHEME:-$ROOT/.build/goscheme}
PYTHON=${PYTHON:-python3}

mkdir -p "$BUILD"

PROGRAMS="fib tail-loop closures globals locals lists vectors strings \
          higher-order mini-eval sort callcc"

best_of() {  # best_of N command...
    n=$1; shift
    best=""
    i=0
    while [ "$i" -lt "$n" ]; do
        t=$("$@")
        if [ -z "$best" ] || [ "$(awk -v a="$t" -v b="$best" 'BEGIN{print (a<b)}')" = "1" ]; then
            best=$t
        fi
        i=$((i + 1))
    done
    echo "$best"
}

echo "C:      $CC $CFLAGS"
echo "C kept: $KEEP"
echo "Python: $($PYTHON --version 2>&1)"
echo

printf '%-13s %10s %10s %10s %10s %7s %7s %7s\n' \
    program 'C -O2' 'C kept' 'Python' 'GoScheme' 'vs-C' 'vs-Ck' 'vs-Py'
printf '%-13s %10s %10s %10s %10s %7s %7s %7s\n' \
    ------- ---------- ---------- ---------- ---------- ------- ------- -------

log_c=0; log_ck=0; log_py=0; n=0
for p in $PROGRAMS; do
    src=$(echo "$p" | tr '-' '_')
    $CC $CFLAGS -o "$BUILD/$p" "$HERE/c/$src.c"
    $CC $CFLAGS $KEEP -o "$BUILD/$p.kept" "$HERE/c/$src.c"
    "$GOSCHEME" compile "$HERE/scheme/$p.scm" -o "$BUILD/$p.scmc"

    # the C and Python programs print their own ms/run; take the best of a few
    a=$(best_of 3 "$BUILD/$p" | awk '{print $3}')
    b=$(best_of 3 sh -c "\"$BUILD/$p.kept\" | awk '{print \$3}'")
    y=$(best_of 3 sh -c "$PYTHON \"$HERE/python/$src.py\" | awk '{print \$3}'")
    c=$("$GOSCHEME" "$HERE/time.scm" "$RUNS" "$GOSCHEME" "$BUILD/$p.scmc")
    c=$(awk -v s="$c" 'BEGIN{printf "%.4f", s*1000}')

    printf '%-13s %9sms %9sms %9sms %9sms %6sx %6sx %6sx\n' "$p" \
        "$a" "$b" "$y" "$c" \
        "$(awk -v x="$c" -v y="$a" 'BEGIN{printf "%.0f", x/y}')" \
        "$(awk -v x="$c" -v y="$b" 'BEGIN{printf "%.0f", x/y}')" \
        "$(awk -v x="$c" -v y="$y" 'BEGIN{printf "%.0f", x/y}')"

    log_c=$(awk -v t="$log_c" -v a="$a" -v c="$c" 'BEGIN{print t + log(c/a)}')
    log_ck=$(awk -v t="$log_ck" -v b="$b" -v c="$c" 'BEGIN{print t + log(c/b)}')
    log_py=$(awk -v t="$log_py" -v y="$y" -v c="$c" 'BEGIN{print t + log(c/y)}')
    n=$((n + 1))
done

echo
echo "geometric mean: $(awk -v t="$log_c" -v n="$n" 'BEGIN{printf "%.0f", exp(t/n)}')x vs C -O2," \
     "$(awk -v t="$log_ck" -v n="$n" 'BEGIN{printf "%.0f", exp(t/n)}')x vs kept C," \
     "$(awk -v t="$log_py" -v n="$n" 'BEGIN{printf "%.1f", exp(t/n)}')x vs Python"
