#!/bin/sh
# SPDX-License-Identifier: MIT
#
# The same twelve workloads as bench/run.sh, but through the other engine:
# `goscheme compile` builds each one natively, and each is timed twice — the
# compiler's output against the same program interpreted.
#
#     bench/run-native.sh [runs]
#
# This is a *different question* from the one run.sh asks, which is why it is a
# separate script rather than another column there.  run.sh measures the
# interpreter against C, Python and Guile, and answers "how fast is this Scheme
# compared with those".  This measures the native compiler against the
# interpreter, and answers "how much does compiling this program buy".
#
# The compiler is a hybrid: it emits machine code for a procedure whose body is a
# computation over its parameters, constants and globals, and hands everything
# else to the runtime.  So the honest expectation is that the answer varies
# wildly by program, and it does — see the table in docs/performance.md.  Only
# some of these programs have anything the compiler can take at all, and the
# "native" column below counts the procedures it emitted, which is the number to
# read first when a ratio looks wrong.
set -e

RUNS=${1:-5}
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/.." && pwd)
BUILD=${BUILD:-$ROOT/.build/bench-native}
GOSCHEME=${GOSCHEME:-$ROOT/.build/goscheme}
OPT=${OPT:--O2}

mkdir -p "$BUILD"

PROGRAMS="fib tail-loop closures globals locals lists vectors strings \
          higher-order mini-eval sort callcc"

# Time one command with bench/time.scm, which is itself a GoScheme program: the
# shell's clock has a 10 ms resolution and most of these finish sooner than that.
timed() {  # timed command...
    t=$("$GOSCHEME" "$HERE/time.scm" "$RUNS" "$@")
    awk -v s="$t" 'BEGIN{printf "%.4f", s*1000}'
}

echo "goscheme: $GOSCHEME"
echo "opt:      $OPT"
echo
printf '%-13s %11s %11s %8s %7s\n' \
    program interpreted compiled ratio native
printf '%-13s %11s %11s %8s %7s\n' \
    ------- ----------- ----------- -------- -------

for p in $PROGRAMS; do
    src=$(echo "$p" | tr '-' '_')

    # Compiling needs the LLVM toolchain and a C compiler; a machine without one
    # is told so once, rather than once per program.
    if ! "$GOSCHEME" compile "$HERE/scheme/$p.scm" "$OPT" -o "$BUILD/$p"; then
        printf '%-13s  %s\n' "$p" "not compiled (see the error above)"
        continue
    fi

    # How many procedures became machine code.  This is the number that explains
    # the ratio beside it: a program with 0 has a compiled *image* and nothing
    # compiled in it.
    n=$("$GOSCHEME" compile "$HERE/scheme/$p.scm" --emit-llvm 2>/dev/null \
        | grep -c '^define %gs.val' || true)

    i=$(timed "$GOSCHEME" "$HERE/scheme/$p.scm")
    c=$(timed "$BUILD/$p")

    printf '%-13s %9sms %9sms %7sx %7s\n' "$p" "$i" "$c" \
        "$(awk -v a="$i" -v b="$c" 'BEGIN{printf "%.2f", a/b}')" "$n"
done

echo
echo "interpreted: the bytecode VM reading the source, which is what runs a file."
echo "compiled:    the same program built by 'goscheme compile $OPT'."
echo "native:      procedures the compiler emitted as machine code, of the ones"
echo "             the program defines.  0 means the ratio is startup, not speed."
