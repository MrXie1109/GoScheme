# Open source

This file says how this project is open, and **what you can expect from it**. It
is not a licence (that is [MIT](../LICENSE)) and it is not a code of conduct. It
is what this repository promises about the way it works, written down so that the
promise can be checked.

## Why write it down

Open source projects say "contributions welcome" easily. What decides whether a
project is actually pleasant to take part in is the detail: whether an issue gets
read, how long a patch waits, whether the maintainer disappears, whether a
one-line pull request is asked to refactor three files first.

Those are things that **can be promised and can be broken**. Written down, they
can at least be raised.

## What this project believes

### Running and checking beats saying

Nothing in this repository claims R7RS support from memory. What is claimed as
supported has tests; what is claimed about performance has a script and measured
numbers.

[docs/performance.md](performance.md) has a section that records **failed
optimizations** — four attempts, all measurably slower, all reverted, with the
reason. A negative result is still a result, and writing it down is more useful
than hiding it: the next person who has the same idea does not have to spend the
day finding out.

### Fewer dependencies, longer life

The interpreter has **no third-party dependencies**: the Go standard library and
nothing else. That is not tidiness, it is longevity. Every dependency is
something that can rot, be abandoned or be attacked. A Scheme interpreter should
still `go build` in ten years.

For the same reason the project keeps **few formats of its own**. There used to be
a compiled file format — `.scmc`, with a version, a symbol table and an
obfuscation pass — and it was removed rather than maintained: compiling a script
takes milliseconds, so a stored form was a second thing to keep, version and
explain for a saving nobody could measure. What a program is stored as now is its
**own source**, packed. That is a smaller promise to keep, and the one this file
can actually stand behind.

### Honesty over looking good

- In the performance comparison we lose to C by **75×**. To Python by 16×.
  Against Guile — Scheme with a compiler, which is the fair opponent — we are
  **1.68×** slower. Those numbers are in the README and the docs, undecorated.
- The **native compiler is partial**, and says so: it compiles a procedure whose
  body is a pure computation and hands everything else to the interpreter, so a
  program runs partly as machine code and partly interpreted. It is **157×** the
  interpreter on `fib` and much less on what it cannot take, and
  [docs/compile.md](compile.md) writes down every limit rather than leaving them
  to be discovered.
- Known limitations have their own section rather than a sentence in small print.

**Writing the weak parts down beats letting people find them.** It also makes the
strong parts believable.

### Backward compatibility is not a slogan

The names of `(goscheme …)` extensions, command line options — changing them
costs something, so:

- a deprecated alias is **kept for a while** and the release notes say until when
  (`channel-open?` was renamed in 2.6.0 and kept "until 3.0.0"; it was in fact
  removed in 3.1.2, later than promised rather than sooner, which is the
  direction a missed deadline should fall);
- something breaks only when the old behaviour was a bug or there is a security
  reason, and the release notes say which.

**v4.0.0 is the exception that proves the rule, and it is worth being plain about
it.** The compiled file format was removed, `goscheme build` became `goscheme
pack`, and `goscheme compile` changed from writing a `.scmc` file to producing a
native executable. Nothing that was promised above survived that: a published
`.scmc` no longer runs, and a script that called `goscheme build` must be
repointed at `goscheme pack`. The release notes say so, in those words.

What made it acceptable is that the thing being removed was **not load-bearing**.
Compiling a script takes milliseconds, so nobody depended on a stored format for
speed; a packed program still runs anywhere the interpreter runs; and the source
was always the thing that mattered. Removing it made the project smaller to
explain and left nothing that a user needed and could not get another way. That
is the test a breaking change has to pass here, and it is written down so the
next one can be held to it too.

### Ship the source, not only the executable

`goscheme pack` produces a standalone executable with the program inside it, and
it is a good way to distribute one: a single file, needing nothing on the machine
that runs it. `goscheme compile` produces a native one. Neither is a reason to
ship *only* the executable.

**When you distribute a GoScheme program, please distribute the `.scm` as well —
next to it, or in the same archive, or in the repository it came from.** This is
a request, not a condition of the licence: MIT lets you ship an executable and
nothing else, and nothing here changes that.

The reason is the thing this project is for. A packed executable runs on one
interpreter; a `.scm` can be read, learned from, fixed by its user, ported to
another Scheme, and still be running in twenty years when this interpreter is
not.

This used to be a harder argument to make, because a compiled file with its names
obfuscated was, deliberately, close to unreadable. **That is gone**, and the
change is worth noticing: a packed program *is* its source with the comments and
the layout taken out, so what it carries is still readable Scheme. Packing is not
obfuscation and is not offered as a way to hide anything — it is a way to stop
paying for a comment at run time. A native executable, by contrast, is machine
code and teaches nobody anything.

So the guidance is:

- **a library or anything meant to be read**: ship the `.scm`, and treat a packed
  executable as a convenience alongside it;
- **an application you want to distribute as one file**: `goscheme pack` is for
  that, and the source is yours to keep — but consider shipping it anyway, or at
  least saying in the program where the source lives, if the program is one
  people will want to learn from. Note that you cannot protect it by packing:
  the packed source is right there in the binary and is not even disguised;
- **inside your own project**: keep the `.scm` in version control and let packed
  or compiled executables be build products, which is also the only way they can
  be rebuilt for a newer version of the interpreter.

## What you can expect

| | |
|---|---|
| An issue | gets read and answered, even if the answer is "not doing this, because …" |
| A pull request | gets a specific review; if the direction is wrong you are told why rather than left guessing |
| A bug report | if it reproduces, a **failing test comes first**, so the bug cannot come back |
| A performance claim | gets measured pinned to a core, best of several, and the numbers go into [docs/performance.md](performance.md) |
| A security report | gets priority |

**Not promised: response time.** This is one person's project and they have a
day job. Slow is possible; ignoring you is not.

## What a contribution should look like

There is no CLA and nothing to sign. Code comes in under MIT and stays MIT.

A few things that make a patch easy to accept:

1. **Bring a test.** A bug fix brings something that reproduces the bug; a
   feature brings something that shows it works. The testing strategy here is
   **running the same program through another implementation and comparing the
   output** — compiled against interpreted against packed against the VM — so
   most new work fits into that net.
2. **Bring a number.** If it is a performance change, give the before and after,
   pinned to a core, best of several. A slower result is welcome too:
   [docs/performance.md](performance.md) has a section for exactly that.
3. **Comments say why, not what.** The code already says what it does. A comment
   should say why it is done this way, what was tried, and why that was not done
   instead. `internal/scheme/vm.go` has a lot of these.
4. **Keep the SPDX line.** Every source file starts with
   `// SPDX-License-Identifier: MIT`; new files do too.
5. **Ask before a large change.** For a new feature worth its own file, open an
   issue first, so that nobody writes something in a direction the project is not
   going.

## We would like to be treated this way too

This project has taken far more than it has given: the R7RS report, the
chibi-scheme reference test suite, the Go standard library, and everyone who
wrote down a mistake they made so that others would not repeat it.

So: **every non-trivial claim in this repository should carry its evidence** — a
piece of code, a test, a measurement, or a link. If you find a conclusion with
nothing behind it, that is a bug, and it is worth reporting.

## This file

It can be changed. If something here is unrealistic, or something you care about
is missing, say so. A promise nobody can keep is worse than none.

---

*This file is a statement of intent, not a licence term; the legal terms are in
[LICENSE](../LICENSE).*
