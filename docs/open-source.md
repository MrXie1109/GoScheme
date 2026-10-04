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

For the same reason the `.scmc` bytecode format is **backward compatible**:
files from versions 1 through 5 all still read, and a version from the future is
refused explicitly rather than misread. The repository keeps a real file from an
older version (`test/bytecode/v4.scmc`) and CI checks that it still reads.

### Honesty over looking good

- In the performance comparison we lose to C by **75×**. To Python by 16×.
  Against Guile — Scheme with a compiler, which is the fair opponent — we are
  **1.68×** slower. Those numbers are in the README and the docs, undecorated.
- The obfuscation feature (`compile -obfuscate`) says in its first sentence that
  **it is not encryption**, and lists what it cannot do.
- Known limitations have their own section rather than a sentence in small print.

**Writing the weak parts down beats letting people find them.** It also makes the
strong parts believable.

### Backward compatibility is not a slogan

Published `.scmc` files, the names of `(goscheme …)` extensions, command line
options — changing them costs something, so:

- a deprecated alias is **kept for a while** and the release notes say until when
  (`channel-open?` was renamed in 2.6.0 and kept "until 3.0.0"; it was in fact
  removed in 3.1.2, later than promised rather than sooner, which is the
  direction a missed deadline should fall);
- when the bytecode format gains a version, **older files still read**, and there
  is a test for it;
- something breaks only when the old behaviour was a bug or there is a security
  reason, and the release notes say which.

### Ship the source, not only the bytecode

`goscheme compile` produces a `.scmc` file, and it is a good way to distribute a
program: it is smaller, it starts without compiling, and `-obfuscate` can strip
the names out of it. None of that is a reason to ship *only* the `.scmc`.

**When you distribute a GoScheme program, please distribute the `.scm` as well —
next to it, or in the same archive, or in the repository it came from.** This is
a request, not a condition of the licence: MIT lets you ship a `.scmc` and
nothing else, and nothing here changes that.

The reason is the thing this project is for. A `.scmc` runs on one interpreter;
a `.scm` can be read, learned from, fixed by its user, ported to another Scheme,
and still be running in twenty years when this interpreter is not. A compiled
file with the names obfuscated is, deliberately, close to unreadable — that is
what it is for — and a program distributed only that way cannot teach anybody
anything or be repaired by anybody but its author.

So the guidance is:

- **a library or anything meant to be read**: ship the `.scm`, and treat the
  `.scmc` as a convenience alongside it;
- **an application you want to protect**: obfuscation is there for that, and the
  source is yours to keep — but consider shipping it anyway, or at least saying
  in the program where the source lives, if the program is one people will want
  to learn from;
- **inside your own project**: keep the `.scm` in version control and let the
  `.scmc` be a build product, which is also the only way the bytecode can be
  rebuilt for a newer version of the interpreter.

Compiled files are readable enough to prove what they do — `scripts/scmc-disassemble.scm`
exists for exactly that — but readable enough to prove is not the same as
readable enough to learn from, and it is the second one that keeps a language
alive.

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
   output** — compiled against interpreted against a bytecode round trip against
   an obfuscated build — so most new work fits into that net.
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
