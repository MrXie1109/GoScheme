# (goscheme match)

Pattern matching over ordinary Scheme data: one expression is compared against
a series of patterns, each of which can bind names, destructure lists and
vectors, and test literals.  It is a special form implemented in Go on top of
the interpreter's own values, so a pattern is not evaluated and the same
symbols, strings, characters, numbers and booleans that appear anywhere else
appear here.  `syntax-rules` cannot express it, because a literal such as `1`
in a pattern would need its own macro rule.

```scheme
(import (scheme base) (goscheme match))
```

## Procedures

### Pattern matching

| Procedure | Arguments | Description |
|---|---|---|
| `match` | `(match expression clause ...)` | Evaluates `expression` **once**, then tries each `clause` in order.  The first clause whose pattern matches, and whose guard (if any) is true, has its body evaluated in a fresh scope holding the pattern's bindings; that value is the value of the whole form.  When no clause matches, an error is raised. |

## Pattern grammar

A clause is `(pattern body ...)`, or `(pattern (guard test) body ...)` when the
body needs a condition.  The pattern is one of the following forms.

* **A wildcard, `_`** — matches anything and binds nothing.  `else` is accepted
  as the same wildcard, for a `cond`-style final clause.
* **Any other symbol** — matches anything, binding the matched value to that
  name in the clause's body and guard.  The symbol is not looked up or
  evaluated.  A name repeated inside one pattern is bound repeatedly and the
  **last** binding wins — it does not require the two values to be equal.
* **A quoted datum, `(quote datum)`** — matches the value `equal?` to `datum`.
  The reader's `'datum` abbreviation produces exactly this form, so `'x` is
  `(quote x)`.
* **A self-evaluating literal** — a number, string, character or boolean
  matches itself, compared with `equal?`.  Exactness is part of the comparison,
  so the literal `5` does not match `5.0` and vice versa.
* **The empty list, `()`** — matches the empty list and nothing else.
* **A proper list, `(p1 p2 ... pn)`** — matches a proper list of exactly `n`
  elements, each subpattern matched against the element in the same position.
* **An improper list, `(p1 p2 ... . prest)`** — the elements match the pair
  chain and `prest` matches whatever tail is left, which may be any value, not
  only a list.
* **A vector, `#(p1 p2 ... pn)`** — matches a vector of exactly `n` elements.
* **Conjunction, `(and p ...)`** — the whole matches when every subpattern
  matches; with no subpatterns it matches anything.  Bindings made by every
  branch are kept.
* **Disjunction, `(or p ...)`** — the first subpattern that matches wins and the
  bindings it made are the ones used; a branch that fails leaves nothing
  behind.  With no subpatterns it matches nothing.
* **Negation, `(not p)`** — matches exactly when `p` does not match, and binds
  nothing.

The names `quote`, `and`, `or` and `not` are recognised only as the first
element of a list pattern, and `quote` and `not` take exactly one operand.  A
bare symbol such as `and` is an ordinary binding pattern, and a list that
genuinely begins with one of those names as data has to be quoted:
`(quote (and 1 2))`.

## Clauses and guards

A clause is `(pattern body ...)` with at least one body expression.  The body is
evaluated left to right in a new environment containing the pattern's bindings
and the value of its last expression is the result.  A guard is written as the
**first** body form, `(pattern (guard test) body ...)`: `test` is evaluated in
the same scope as the body, and when it is false the search continues with the
following clauses rather than raising.  A guarded clause still needs a body
after the guard.

The guard is a sibling of the pattern, not part of it.  `(n (guard (> n 5))
'big)` guards the pattern `n`, while `((n (guard #t)) body)` is a two-element
list pattern whose elements are the symbol `n` and the list `(guard #t)` — the
parentheses are the whole difference.  A body that genuinely starts with a
one-clause guard expression should be wrapped in `begin`.

## Notes

* When no clause matches, a generic error object is raised whose message is
  `match: no pattern matched <datum>`; it is an ordinary condition, so `guard`
  catches it and `error-object?` reports `#t`.  A malformed pattern raises the
  same kind of condition: `match: a clause is (pattern body ...)`,
  `match: expected an expression and at least one clause`,
  `match: quote takes one datum`, `match: (guard test) takes one expression`,
  `match: a guarded clause needs a body`, and `match: not takes one pattern`.
* Clauses are parsed as they are reached, so a malformed pattern in a later
  clause is never reported when an earlier clause has already matched.  A guard
  that raises propagates its condition instead of falling through.
* Literal patterns use the interpreter's `equal?`, so lists and vectors compare
  elementwise, while `5` and `5.0` are different literals.
* `match` is a special form, not a procedure: it is usable wherever an
  expression is expected (an argument, a `let` or `define` right-hand side, and
  so on), but it cannot be passed to `map`, stored in a variable or aliased.
* There is no ellipsis repetition and no "rest" inside a vector pattern: list
  and vector patterns have a fixed shape, and a predicate test that is not an
  equality test belongs in a guard.
* This library is pure Go with no FFI dependency, so it is present in every
  build, static and dynamic alike.

## Example

```scheme
(import (scheme base) (scheme write) (goscheme match))

(define (describe x)
  (match x
    (()                    'empty)
    (#(a b)                (list 'vector a b))
    ((or 1 2 3)            'small)
    (n (guard (number? n)) (* n 10))
    ((a b . rest)          (list 'list a b rest))
    ((a . b)               (list 'dotted a b))
    (else                  (list 'other x))))

(write (describe '()))         (newline)   ; => empty
(write (describe #(1 2)))      (newline)   ; => (vector 1 2)
(write (describe 2))           (newline)   ; => small
(write (describe 7))           (newline)   ; => 70
(write (describe '(1 2 3 4)))  (newline)   ; => (list 1 2 (3 4))
(write (describe '(1 . 2)))    (newline)   ; => (dotted 1 2)
(write (describe "s"))         (newline)   ; => (other "s")
```
