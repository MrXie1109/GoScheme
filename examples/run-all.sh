#!/bin/sh
# SPDX-License-Identifier: MIT
# Run every example and report which ones fail.
#
#   ./examples/run-all.sh
#
# To use a particular interpreter, set GOSCHEME, for example:
#   GOSCHEME=./dist/goscheme-linux-amd64 ./examples/run-all.sh

set -u

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(dirname -- "$here")

if [ -n "${GOSCHEME:-}" ]; then
    gs=$GOSCHEME
elif command -v goscheme >/dev/null 2>&1; then
    gs=goscheme
elif [ -x "$root/goscheme" ]; then
    gs=$root/goscheme
else
    echo "run-all.sh: no interpreter found; set GOSCHEME=/path/to/goscheme" >&2
    exit 2
fi

total=0
failed=0

run() {
    name=$1
    shift
    total=$((total + 1))
    printf '%-24s ' "$name"
    if out=$("$gs" "$@" 2>&1); then
        printf 'ok\n'
    else
        failed=$((failed + 1))
        printf 'FAILED\n'
        printf '%s\n' "$out" | sed 's/^/    /'
    fi
}

run hash-tables.scm    "$here/hash-tables.scm"
run processes.scm      "$here/processes.scm"
run script-args.scm    "$here/script-args.scm" alpha beta
run numbers.scm        "$here/numbers.scm"
run recursion.scm      "$here/recursion.scm"
run concurrency.scm    "$here/concurrency.scm"
run tcp-server.scm     "$here/tcp-server.scm"
run http-server.scm    "$here/http-server.scm"
run pipes.scm          "$here/pipes.scm"
run sync.scm           "$here/sync.scm"
run json.scm           "$here/json.scm"
run regexp.scm         "$here/regexp.scm"
run time.scm           "$here/time.scm"
run fs.scm             "$here/fs.scm"
run match.scm          "$here/match.scm"
run fast.scm           "$here/fast.scm"
run libraries/main.scm "$here/libraries/main.scm"

echo
if [ "$failed" -eq 0 ]; then
    echo "$total examples ran"
    exit 0
fi
echo "$failed of $total examples failed" >&2
exit 1
