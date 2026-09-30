# The GoScheme manual

A guided walk through the interpreter and its libraries, in one file, in both
languages:

| | English | 中文 |
|---|---|---|
| The guide | [guide.md](guide.md) | [指南](guide.zh.md) |

It covers, in order: running the interpreter · the language in one page ·
numbers · data and text · concurrency · I/O and the operating system ·
performance · embedding and `goscheme build` · pitfalls · where to go next.

Every example in it was run against the interpreter, and the `; =>` comments
are what it printed.

For the terse version instead:

* [main README](../README.md) / [README 中文](../README_zh.md) — the reference:
  language coverage, implementation notes, measurements, testing, cross-compilation.
* [`docs/extensions/`](../extensions/README.md) — one page per `(goscheme ...)`
  library, every procedure with its arguments and errors.
* [`docs/srfi/`](../srfi/README.md) — the SRFI libraries.
* [`examples/`](../../examples/README.md) — runnable programs, one theme each.

Conventions used in the guide:

* `./.build/goscheme` is the interpreter `make build` produces; a released
  binary is named `goscheme-linux-amd64`, `goscheme-darwin-arm64`,
  `goscheme-windows-amd64.exe` and so on.
* `; => value` after an expression is what the interpreter prints for it.
