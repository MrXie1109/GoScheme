# (goscheme regexp)

The `(goscheme regexp)` library puts Go's regular-expression engine — RE2 —
behind Scheme, so patterns use Go's RE2 syntax and matching runs in time linear
in the input.  RE2 gives up backreferences and lookaround, which means a
pattern from an untrusted source cannot make the interpreter hang.  Match
offsets are exact integers and replacement strings use Go's `$1` expansion.

```scheme
(import (scheme base) (goscheme regexp))
```

## Procedures

### Compiling and testing

| Procedure | Arguments | Description |
|---|---|---|
| `regexp` | `(regexp pattern)` | Compiles pattern, a string in RE2 syntax, and returns a compiled regexp object.  Raises an error if the engine rejects the pattern, for example on a backreference or an unbalanced group. |
| `regexp?` | `(regexp? obj)` | Returns `#t` if obj is a compiled regexp and `#f` for anything else, including a pattern string. |
| `regexp-match?` | `(regexp-match? pattern string)` | Returns `#t` if string contains at least one match for pattern, else `#f`. |

### Matching and positions

| Procedure | Arguments | Description |
|---|---|---|
| `regexp-match` | `(regexp-match pattern string)` | Returns `#f` if there is no match; otherwise a list whose first element is the whole match and whose remaining elements are the capture groups, all as strings.  A group that did not participate in the match is the empty string, following Go's submatch convention. |
| `regexp-match-positions` | `(regexp-match-positions pattern string)` | Returns `#f` if there is no match; otherwise a list of `(start . end)` pairs of exact integers, the first for the whole match and then one per capture group.  A group that did not participate is `#f`.  Offsets are byte offsets into the UTF-8 encoding, with end exclusive. |

### Replacing and splitting

| Procedure | Arguments | Description |
|---|---|---|
| `regexp-replace` | `(regexp-replace pattern string replacement)` | Replaces the first match in string with replacement and returns the result; if there is no match, returns string unchanged.  In replacement, `$1`, `$2`, ... and `${name}` expand to capture groups, and `$$` is a literal dollar sign. |
| `regexp-replace-all` | `(regexp-replace-all pattern string replacement)` | Like the single replacement, but replaces every non-overlapping match, using the same `$1` / `${name}` / `$$` expansion. |
| `regexp-split` | `(regexp-split pattern string)` | Returns a list of the substrings of string between matches.  A match at either end contributes an empty string, and a pattern that matches nothing returns a one-element list holding the whole string. |

## Notes

* Every procedure that takes pattern accepts either a compiled regexp or a
  pattern string; a bare string is compiled on each call, so compile once with
  the constructor when a pattern is used in a loop.
* The engine is RE2, not a backtracking engine: backreferences such as `\1`,
  lookahead and lookbehind are unsupported and raise at compile time rather
  than matching differently.  In exchange, matching time is linear, so nested
  quantifiers such as `(a+)+` cannot blow up.
* Positions are byte offsets, not character offsets, so a multi-byte UTF-8
  character counts as several bytes; matching and splitting themselves are
  Unicode-aware and operate on code points.
* A capture group that did not participate is reported differently by the two
  match procedures: the empty string from the string match, and `#f` from the
  position match.
* A non-regexp, non-string pattern, a non-string subject, or a bad pattern
  string raises an ordinary Scheme condition, catchable with `guard`.

## Example

```scheme
(import (scheme base) (scheme write) (goscheme regexp))

(define date (regexp "(\\d{4})-(\\d{2})-(\\d{2})"))

(write (regexp-match date "on 2024-05-17."))            ; => ("2024-05-17" "2024" "05" "17")
(write (regexp-match-positions date "on 2024-05-17."))  ; => ((3 . 13) (3 . 7) (8 . 10) (11 . 13))
(write (regexp-replace date "on 2024-05-17." "$3/$2/$1")) ; => "on 17/05/2024."
(write (regexp-split "," "a,b,,c"))                     ; => ("a" "b" "" "c")
```
