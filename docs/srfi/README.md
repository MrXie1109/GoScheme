# SRFI reference

The SRFI libraries this implementation provides, one page each, with every
exported name, its arguments and what it does.  They follow the same convention
as [`docs/extensions/`](../extensions/README.md): the argument list is written
the way the procedure is called, optional arguments are in brackets, a trailing
`...` repeats, and a `!` at the end of a name means the procedure is allowed to
write into its argument.

```scheme
(import (scheme base) (srfi 1))
```

| Library | Names | What it is | Page |
|---|---|---|---|
| `(srfi 1)` | 97 | the list library: constructors, predicates, selectors, folds and unfolds, filtering, searching, deleting, association lists and the `lset-` set operations | [1.md](1.md) |
| `(srfi 2)` | 1 | `and-let*` | [2.md](2.md) |
| `(srfi 8)` | 1 | `receive` | [8.md](8.md) |
| `(srfi 26)` | 2 | `cut` and `cute` | [26.md](26.md) |
| `(srfi 111)` | 4 | boxes | [111.md](111.md) |
| `(srfi 128)` | 30 | comparators, with `hash-bound` and `hash-salt` as parameters | [128.md](128.md) |
| `(srfi 133)` | 38 | the vector library | [133.md](133.md) |

Two conventions matter when reading the pages:

* **Shared bindings.**  Where a name already exists in R7RS or in
  `(goscheme fast)`, the two libraries export the *same* binding rather than a
  copy, because a later `import` shadows an earlier one and two bindings for
  one name would make the meaning depend on import order.  `(srfi 1)` shares
  sixteen names with `(goscheme fast)`; `(srfi 133)` shares fifteen with
  `(scheme base)` and four with `(goscheme fast)`.  Where the SRFI specifies
  more than the fast library did, the fast procedure was widened (a range for
  `vector-reverse!`, a three-valued comparison for `vector-binary-search`).
* **Linear-update procedures.**  SRFI-1 and SRFI-133 allow the procedures
  whose names end in `!` to be pure, and here most of them are: `take!` is
  `take`, `filter!` is `filter`, and so on.  Two of them really do reuse the
  pairs or the vector: `reverse!` and `append!` for lists, and the vector
  mutators.

The test suite keeps these pages honest: a library that exports a name its page
does not mention fails `go test ./internal/scheme`.
