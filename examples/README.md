# GoScheme examples

Small, runnable programs that show what this Scheme does.  Most of the language
is R7RS-small, so the interesting part is where it goes beyond the report: a Go
flavoured concurrency library, hash tables, running other programs, and the
`goscheme build` packaging step.

Every example is a normal script, and each one prints what it is doing, so they
are meant to be read as much as run.

## Running them

With `goscheme` on your `PATH`:

```console
$ goscheme examples/numbers.scm
```

With a binary from `dist/`:

```console
$ ./dist/goscheme-linux-amd64 examples/numbers.scm
```

Or all of them at once, which reports the ones that fail:

```console
$ ./examples/run-all.sh
$ GOSCHEME=./dist/goscheme-linux-amd64 ./examples/run-all.sh
```

## What each one shows

| File | What it demonstrates |
|---|---|
| `numbers.scm` | The numeric tower: exact integers of any size, exact rationals, complex numbers, and the number syntax the reader accepts (`#x1f`, `#e1.5`, the `s f d l` exponent markers). |
| `recursion.scm` | Proper tail calls, so loops of a million turns run in constant stack space, including mutual recursion and tail position in `cond`. |
| `hash-tables.scm` | `(goscheme hash-table)`: the three key comparisons, `ref` versus `ref/default`, mutation, walking, and `hash` with a bound. |
| `concurrency.scm` | `(goscheme channel)`: buffered and unbuffered channels, `chan-recv!`'s two values, worker pools, `(go ...)`, `go-wait`, and `(select ...)` with `(after ms)` and `(else)`. |
| `processes.scm` | `(goscheme process)`: `system` through the shell versus `system*` directly, exit statuses including signal deaths, inherited standard streams, and a missing program as a file error. |
| `script-args.scm` | `(command-line)` and why the interpreter's name is not in it, `(assert ...)` as a catchable condition, `#!unspecified`, and `(features)`. |
| `libraries/main.scm` | Importing libraries from files: `(lib greet)` is read from `lib/greet.sld`, and `(lib namer)` imports it in turn. |
| `tcp-server.scm` | `(goscheme socket)` and `(goscheme sync)`: one thread accepts, one thread per connection echoes, and three clients talk to it at once. |
| `http-server.scm` | `(goscheme http)`: routes, a JSON response, a header check, a 500 from a handler that raises, and the client half. |
| `pipes.scm` | `(goscheme process)`: two children joined into a pipeline with `open-input-process` and `open-output-process`. |
| `sync.scm` | `(goscheme sync)`: a counter under a lock, the same with an atomic, a wait group, and a one-time run. |
| `json.scm` | `(goscheme json)`: parsing a document, reading hash tables and vectors out of it, writing it back, and the null and big-integer cases. |
| `regexp.scm` | `(goscheme regexp)`: capture groups, positions, `$1` in a replacement, splitting, and a reused compiled pattern. |
| `time.scm` | `(goscheme time)`: sleeping and measuring it, formatting, parsing, and the UTC parts of an instant. |
| `fs.scm` | `(goscheme fs)`: building a temporary tree, globbing it, walking it, the path helpers, and removing it again. |
| `match.scm` | `(goscheme match)`: a small arithmetic evaluator written as patterns, plus destructuring and guards. |
| `fast.scm` | `(goscheme fast)`: the same sort, filter, string search and vector addition written in Scheme next to the Go versions, timed, then a tour of the whole library — list helpers, statistics, primes and factoring, batch vector arithmetic, text, hashing and randomness.  The full reference is in [docs/extensions/fast.md](../docs/extensions/fast.md). |
| `embed/main.go` | A Go program using the interpreter as a library — `go run ./examples/embed`. |

## Libraries from files

A library lives in the file its name describes, with no registry to update:

```scheme
(import (lib greet))        ; reads lib/greet.sld, or .scm, .sls or .ss
```

The search starts at the directory of the file doing the importing, and each
library that gets loaded adds its own directory, so a library can import its
neighbours.  After that come the directories in `GOSCHEME_LIBRARY_PATH`
(separated by `:` on Unix, `;` on Windows) and finally the current directory.
That is why `examples/libraries/main.scm` runs the same way from the repository
root, from its own directory, or by absolute path from anywhere.

## Turning one into a single executable

`goscheme build` binds a script to a copy of the interpreter, and `-static` also
folds in every library and `include` it needs, so the result runs where none of
those files exist:

```console
$ goscheme build -static examples/libraries/main.scm -o hello
$ cd / && /path/to/hello
hello from a library, world
```

Without `-static`, the executable still reads its libraries from disk at run
time, which is what you want while developing them.  See the README in the
repository root for the full `build` story, and `docs/ffi-design.md` for why
calling into C is the one thing these binaries deliberately do not do.
