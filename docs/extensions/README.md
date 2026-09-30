# Extension library reference

GoScheme implements R7RS-small, and then adds the libraries below on top.
Each one has its own page here with the import line, every exported name, its
arguments and what it does — a lookup reference rather than a tutorial.  The
[main README](../README.md) describes the same libraries in prose, and
`examples/` has runnable programs that use them.

```scheme
(import (scheme base) (goscheme fast))   ; for example
```

| Library | Names | What it is for | Page |
|---|---|---|---|
| `(goscheme fast)` | 158 | the jobs Scheme does slowly — sorting, bulk vector arithmetic, strings, hashing, number theory — done in Go | [fast.md](fast.md) |
| `(goscheme channel)` | 9 | Go-style concurrency: channels, `(go ...)`, `go-wait`, `select` | [channel.md](channel.md) |
| `(goscheme sync)` | 22 | the other half of concurrency: mutexes, wait groups, once, atomic counters | [sync.md](sync.md) |
| `(goscheme socket)` | 8 | TCP listeners and connections, which are ordinary ports | [socket.md](socket.md) |
| `(goscheme http)` | 26 | an HTTP client and `net/http`-backed server | [http.md](http.md) |
| `(goscheme json)` | 2 | JSON to and from Scheme data | [json.md](json.md) |
| `(goscheme regexp)` | 8 | regular expressions on Go's RE2 engine | [regexp.md](regexp.md) |
| `(goscheme time)` | 6 | sleeping, clocks, parsing and formatting times | [time.md](time.md) |
| `(goscheme fs)` | 13 | globbing, directory walking and path manipulation | [fs.md](fs.md) |
| `(goscheme process)` | 5 | running programs and talking to their pipes | [process.md](process.md) |
| `(goscheme hash-table)` | 23 | hash tables, with `eq?`, `eqv?` and `equal?` flavours | [hash-table.md](hash-table.md) |
| `(goscheme match)` | 1 | the `match` pattern-matching special form | [match.md](match.md) |
| `(goscheme ffi)` | 3 | calling into a C shared library — cgo builds only | [ffi.md](ffi.md) |

## How to read the tables

* **Arguments** are written the way the procedure is called, with the optional
  ones in brackets: `(sort list [less?])`.  A trailing `...` means the argument
  repeats: `(bit-and n ...)` accepts any number of them.
* **Errors** are ordinary Scheme conditions, so they can be caught:

  ```scheme
  (guard (e (#t 'caught)) (vector-add #(1) #(1 2)))   ; => caught
  ```

  The message names the procedure and what it expected.
* **A sequence** — in `(goscheme fast)` — is either a proper list or a vector.
  Where the distinction matters the description says which one.
* **An `!` at the end of a name** means the procedure works in place, writing
  into the sequence it was given, and returns it.  Everything else builds a
  new one.
* **Threads.**  The interpreter's own shared state is locked, so channels,
  mutexes, atomics, ports and the randomness procedures are safe to use from
  several interpreter threads.  Ordinary Scheme data — pairs, strings, vectors,
  records — is **not** synchronized; share memory by communicating, not the
  other way around.
* **Build flavours.**  `(goscheme ffi)` exists only in cgo builds.  The
  binaries attached to a release are static, so they have every library except
  that one; `scripts/build-dist.sh` builds the dynamic flavours too.  See
  [Cross-compilation](../../README.md#cross-compilation) in the main README.

The export lists in these pages are checked against the interpreter: the
`internal/scheme` test suite fails if a library exports a name that its page
does not mention.
