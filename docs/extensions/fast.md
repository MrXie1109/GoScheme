# (goscheme fast)

The jobs Scheme does slowly, done in Go: 158 procedures for ordering,
sequences, selection, folds, aggregates, batch arithmetic on vectors, number
theory, strings, bytes and randomness.  The loop, the indexing, the copying and
the sorting stay in Go, and a comparison or predicate that is a builtin is
applied without a call back into Scheme.

```scheme
(import (scheme base) (goscheme fast))
```

Two things are worth knowing before the tables:

* **The fast lane.**  Every procedure that takes a `pred`, a `key` or a `less?`
  checks whether it was handed a builtin it knows — `pred`: `even?`, `odd?`,
  `zero?`, `positive?`, `negative?`, `number?`, `integer?`, `string?`,
  `symbol?`, `char?`, `boolean?`, `pair?`, `null?`, `list?`, `vector?`, `not`;
  `less?`: `<`, `<=`, `>`, `>=`, `=`, `string<?`, `string<=?`, `string>?`,
  `string>=?`, `string=?`, `string-ci<?`, `char<?`, `char=?`, `equal?`, `eqv?`,
  `eq?`.  A builtin runs inside the Go loop.  A procedure you wrote yourself is
  called once per element (once per element for `sort-by`'s key, rather than
  once per comparison), which is roughly what the equivalent Scheme loop costs,
  so in that case the library is a convenience rather than a speed-up.
* **Sequences.**  Where a procedure says *sequence* it accepts either a proper
  list or a vector.  The ones that build a result list return a list whichever
  it was given; the ones that build a vector say so.

Errors are ordinary Scheme conditions, so they can be caught with `guard`, and
the message names the procedure.  Measured numbers are in the Performance
section of the README, and `examples/fast.scm` times itself.

## Ordering

| Procedure | Arguments | Description |
|---|---|---|
| `sort` | `(sort list [less?])` | Returns a **new** sorted list.  The sort is stable and `less?` defaults to `<`, so equal elements keep their order.  Works on lists only. |
| `sort!` | `(sort! vector [less?])` | Sorts a vector in place and returns it. |
| `sort-by` | `(sort-by key list [less?])` | Sorts a list by `(key element)`: the key procedure runs **once per element**, not once per comparison.  `less?` compares the keys and defaults to `<`. |
| `vector-sort` | `(vector-sort vector [less?])` | Returns a new sorted vector. |
| `vector-sort-by` | `(vector-sort-by key vector [less?])` | Like `sort-by`, for a vector. |
| `vector-binary-search` | `(vector-binary-search vector key [less?])` | The index of `key` in a vector sorted by the same `less?`, or `#f`.  It finds the lower bound and then checks the element there, so a duplicate key reports the first occurrence. |
| `vector-binary-search-insert` | `(vector-binary-search-insert vector key [less?])` | The index at which `key` would be inserted to keep the vector sorted: the lower bound, in `0..(vector-length vector)`. |

## Sequences

| Procedure | Arguments | Description |
|---|---|---|
| `iota` | `(iota count [start [step]])` | A list of `count` numbers starting at `start` (0) and adding `step` (1).  Built in one Go pass instead of one `cons` per interpreted step. |
| `vector-iota` | `(vector-iota count [start [step]])` | The same as `iota`, as a vector. |
| `range` | `(range start end [step])` | A list of numbers from `start` up to but **not** including `end`.  `step` may be negative (to count down) or an exact rational; a zero step is an error. |
| `take` | `(take n list)` | The first `n` elements.  An error when the list is shorter. |
| `drop` | `(drop n list)` | Everything but the first `n` elements. |
| `take-right` | `(take-right n list)` | The last `n` elements, in order. |
| `drop-right` | `(drop-right n list)` | Everything but the last `n` elements. |
| `split-at` | `(split-at n list)` | Returns two values: `(take n list)` and `(drop n list)`. |
| `last` | `(last sequence)` | The final element of a non-empty sequence. |
| `chunk` | `(chunk list n)` | A list of lists of `n` elements each; the final chunk may be short.  `n` must be at least 1. |
| `vector-take` | `(vector-take n sequence)` | A new vector with the first `n` elements. |
| `vector-drop` | `(vector-drop n sequence)` | A new vector with everything but the first `n` elements. |
| `vector-chunk` | `(vector-chunk vector n)` | A list of vectors of `n` elements each. |
| `vector-concat` | `(vector-concat list-of-vectors)` | One new vector holding all of them, in order. |
| `vector-reverse` | `(vector-reverse vector)` | A new reversed vector. |
| `vector-reverse!` | `(vector-reverse! vector)` | Reverses in place and returns the vector. |
| `vector-swap!` | `(vector-swap! vector i j)` | Exchanges two elements in place; an index out of range is an error.  Returns an unspecified value. |

## Selection

| Procedure | Arguments | Description |
|---|---|---|
| `filter` | `(filter pred sequence)` | A list of the elements `pred` accepts. |
| `filter-not` | `(filter-not pred sequence)` | A list of the elements `pred` rejects. |
| `vector-filter!` | `(vector-filter! pred vector)` | Compacts the accepted elements to the front of the vector, in place, and returns how many were kept.  The elements after that count keep their old values. |
| `count` | `(count pred sequence)` | How many elements satisfy `pred`. |
| `any` | `(any pred sequence-or-list ...)` | The first value the predicate returns that counts as true, or `#f`.  With more than one list it is SRFI-1's `any`, applying the predicate to one element of each; a builtin predicate is applied in Go.  This is the same binding `(srfi 1)` exports. |
| `every` | `(every pred sequence-or-list ...)` | The last value the predicate returned when every element satisfies it, or `#f`; `#t` for an empty sequence.  Also SRFI-1's, and shared with `(srfi 1)`. |
| `find` | `(find pred list)` | The first element accepted, or `#f`. |
| `list-index` | `(list-index pred list)` | The index of the first element accepted, or `#f`. |
| `delete` | `(delete x sequence [equal?])` | A list without the elements equal to `x`. |
| `delete-duplicates` | `(delete-duplicates list [equal?])` | Keeps the first of each group of equal elements, in order.  Atoms go through a map keyed by printed form — a shortcut that is only sound for `equal?`, `eqv?` and `eq?`, the comparisons under which two atoms with different printed forms are never equal; any other comparison falls back to the linear scan that defines the procedure. |
| `partition` | `(partition pred list)` | Returns two values: the matching elements and the rest, each in order. |
| `vector-partition` | `(vector-partition pred vector)` | Returns two values: a new vector holding the accepted elements followed by the rejected ones, and how many were accepted.  This is SRFI-133's shape (`(srfi 133)` exports the same binding), and it replaced the earlier two-vector result in 3.0. |
| `vector-index-of` | `(vector-index-of vector x [equal?])` | The index of the first element equal to `x`, or `#f`. |
| `zip` | `(zip sequence ...)` | A list of the rows of several sequences, stopping at the shortest one. |
| `unzip` | `(unzip rows)` | Transposes rows (the inverse of `zip`), stopping at the narrowest row. |
| `flatten` | `(flatten sequence)` | A list of the atoms in a nested structure of lists and vectors, in order.  Nesting deeper than 10,000 levels (which a circular structure would reach) is an error rather than a hang. |

## Folding and association

| Procedure | Arguments | Description |
|---|---|---|
| `fold-left` | `(fold-left proc init sequence)` | `(proc (proc init x1) x2) ...`.  When `proc` is one of `+`, `-`, `*`, `/`, `max`, `min` the fold stays in Go. |
| `fold-right` | `(fold-right proc init sequence)` | Applies `proc` to each element and the accumulated value from the right: `(proc x1 (proc x2 ... init))`.  The same builtins stay in Go. |
| `assoc-set` | `(assoc-set alist key value [equal?])` | A new association list: the entry for `key` is replaced where it is, or a `(key . value)` pair is appended when it is missing.  The original list is not modified. |

## Aggregates and statistics

| Procedure | Arguments | Description |
|---|---|---|
| `sum` | `(sum sequence)` | The total, `0` for an empty sequence.  Stays in the numeric tower, so exact in, exact out. |
| `product` | `(product sequence)` | The product, `1` for an empty sequence. |
| `min-of` | `(min-of sequence)` | The smallest number; an empty sequence is an error. |
| `max-of` | `(max-of sequence)` | The largest number. |
| `vector-dot` | `(vector-dot a b)` | The inner product.  Vectors of different lengths are an error. |
| `vector-norm` | `(vector-norm vector)` | The Euclidean norm, `sqrt` of the dot product with itself, as an inexact number. |
| `vector-argmin` | `(vector-argmin vector [less?])` | The index of the first element that is smallest under `less?` (default `<`); an empty vector is an error. |
| `vector-argmax` | `(vector-argmax vector [less?])` | The index of the first largest element. |
| `mean` | `(mean sequence)` | The arithmetic mean.  Exact over exact numbers, so `(mean '(1 2))` is `3/2`. |
| `median` | `(median sequence)` | Sorts a copy in Go and takes the middle element, dividing the sum of the two middle ones when the count is even. |
| `percentile` | `(percentile sequence p)` | The nearest-rank percentile: the element at rank `ceil(p/100 * n)`, so `p` of 0 and 1 give the smallest element and 100 the largest. |
| `variance` | `(variance sequence)` | The population variance (divides by `n`), computed and returned as an inexact number. |
| `stddev` | `(stddev sequence)` | The square root of `variance`. |
| `mode` | `(mode sequence)` | The most frequent element, the first one to reach that count when there is a tie.  Elements are counted by printed form. |

## Batch arithmetic on vectors

These are the batch lane: one Go pass over the whole vector instead of one
interpreted step per element.  The `!` variants write into the vector they were
given and return it, so a numeric inner loop allocates nothing.  Exactness goes
through the numeric tower (`vector-div` of exact integers gives exact
rationals), and the two-vector procedures require equal lengths.

| Procedure | Arguments | Description |
|---|---|---|
| `vector-add` | `(vector-add a b)` | A new vector with `a[i] + b[i]`. |
| `vector-sub` | `(vector-sub a b)` | A new vector with `a[i] - b[i]`. |
| `vector-mul` | `(vector-mul a b)` | The element-wise (Hadamard) product. |
| `vector-div` | `(vector-div a b)` | The element-wise quotient. |
| `vector-scale` | `(vector-scale vector k)` | A new vector with every element multiplied by `k`. |
| `vector-negate` | `(vector-negate vector)` | A new vector with every element negated. |
| `vector-abs` | `(vector-abs vector)` | A new vector of magnitudes. |
| `vector-clamp` | `(vector-clamp vector low high)` | A new vector with every element kept inside `[low, high]`. |
| `vector-prefix-sum` | `(vector-prefix-sum vector)` | The running totals: element `i` is the sum of the elements up to and including `i`. |
| `vector-add!` | `(vector-add! a b)` | In place: `a[i] += b[i]`, returns `a`. |
| `vector-sub!` | `(vector-sub! a b)` | In place: `a[i] -= b[i]`, returns `a`. |
| `vector-mul!` | `(vector-mul! a b)` | In place element-wise product, returns `a`. |
| `vector-div!` | `(vector-div! a b)` | In place element-wise quotient, returns `a`. |
| `vector-scale!` | `(vector-scale! vector k)` | In place scaling, returns the vector. |
| `vector-negate!` | `(vector-negate! vector)` | In place negation, returns the vector. |
| `vector-abs!` | `(vector-abs! vector)` | In place magnitudes, returns the vector. |
| `vector-clamp!` | `(vector-clamp! vector low high)` | In place clamp, returns the vector. |
| `vector-equal?` | `(vector-equal? vector ...)` | `#t` when all the vectors have the same length and `equal?` elements.  Accepts one or more (up to 32) vectors. |
| `vector-compare` | `(vector-compare a b [less?])` | The lexicographic order as `-1`, `0` or `1`; a shorter vector that is a prefix comes first. |

## Number theory and bits

The bit procedures accept exact integers of any size.

| Procedure | Arguments | Description |
|---|---|---|
| `bit-and` | `(bit-and n ...)` | Bitwise AND; with no arguments the identity `-1`. |
| `bit-or` | `(bit-or n ...)` | Bitwise OR; with no arguments `0`. |
| `bit-xor` | `(bit-xor n ...)` | Bitwise XOR; with no arguments `0`. |
| `bit-not` | `(bit-not n)` | Bitwise complement, so `(bit-not 0)` is `-1`. |
| `bit-shift` | `(bit-shift n count)` | Shifts left by a positive count, and right (keeping the sign) by a negative one.  Counts beyond 100,000,000 are refused rather than allocating without bound. |
| `bit-count` | `(bit-count n)` | The number of set bits in the magnitude of `n`. |
| `integer-length` | `(integer-length n)` | The number of bits in the magnitude of `n`, so `0` for zero. |
| `expt-mod` | `(expt-mod base exponent modulus)` | Modular exponentiation; no huge intermediate power is built.  The exponent must not be negative and the modulus must be positive. |
| `isqrt` | `(isqrt n)` | The integer square root, the floor of the real one; a negative argument is an error. |
| `prime?` | `(prime? n)` | True for primes.  Baillie-PSW with 20 extra Miller-Rabin rounds, so the answer is deterministic for any input a program will meet in practice; `n` below 2 is `#f`. |
| `primes` | `(primes limit)` | A list of the primes below `limit`, sieved in Go.  Limits above 50,000,000 are refused. |
| `factor` | `(factor n)` | The prime factorisation of a positive `n`, ascending and with multiplicity; `(factor 1)` is the empty list, and zero or a negative argument is an error.  Small factors are divided out first and Pollard's rho splits the rest. |
| `clamp` | `(clamp x low high)` | `x` kept inside `[low, high]`; a low bound above the high one is an error. |
| `sign` | `(sign x)` | `-1`, `0` or `1`. |

## Strings

All indices and lengths count **characters**, not bytes.  Nothing truncates
implicitly: the padding procedures return the string unchanged when it is
already wide enough.

| Procedure | Arguments | Description |
|---|---|---|
| `string-split` | `(string-split string [separator [limit]])` | Splits on `separator`, which defaults to `""` (one piece per character).  With a `limit` of `n` only the first `n-1` separators are used and the last piece keeps the rest, so `(string-split "a:b:c" ":" 2)` is `("a" "b:c")`. |
| `string-join` | `(string-join list [separator])` | Concatenates the strings in the list, putting `separator` (default `""`) between them. |
| `string-contains` | `(string-contains string substring)` | The character index of the first occurrence, or `#f`; the empty substring is at 0. |
| `string-index` | `(string-index string char)` | The character index of a character, or `#f`. |
| `string-index-from` | `(string-index-from string char-or-substring start)` | The first occurrence at or after the character index `start`. |
| `string-last-index` | `(string-last-index string char-or-substring)` | The last occurrence, or `#f`. |
| `string-find-all` | `(string-find-all string substring)` | A list of every non-overlapping occurrence; an empty substring gives the empty list. |
| `string-count` | `(string-count string char-or-substring)` | How many non-overlapping occurrences there are. |
| `string-prefix?` | `(string-prefix? string prefix)` | Whether the string starts with the prefix. |
| `string-suffix?` | `(string-suffix? string suffix)` | Whether the string ends with the suffix. |
| `string-prefix-ci?` | `(string-prefix-ci? string prefix)` | The same, ignoring case. |
| `string-suffix-ci?` | `(string-suffix-ci? string suffix)` | The same, ignoring case. |
| `string-trim` | `(string-trim string [cutset])` | Removes whitespace (or any character in `cutset`) from both ends. |
| `string-trim-left` | `(string-trim-left string [cutset])` | The same, from the left only. |
| `string-trim-right` | `(string-trim-right string [cutset])` | The same, from the right only. |
| `string-replace` | `(string-replace string from to)` | Replaces every occurrence; an empty `from` is an error. |
| `string-replace-first` | `(string-replace-first string from to)` | Replaces one occurrence. |
| `string-pad-left` | `(string-pad-left string width [char])` | Pads on the left to `width` characters with `char` (default a space). |
| `string-pad-right` | `(string-pad-right string width [char])` | Pads on the right. |
| `string-pad-center` | `(string-pad-center string width [char])` | Centres the text, putting the odd extra character on the right. |
| `string-reverse` | `(string-reverse string)` | The characters in reverse order. |
| `string-repeat` | `(string-repeat string count)` | The string repeated `count` times. |
| `string-chunk` | `(string-chunk string n)` | A list of strings of `n` characters each; the last may be short. |
| `string-sort` | `(string-sort string)` | The characters sorted by code point. |
| `string-take` | `(string-take string n)` | The first `n` characters; an error when the string is shorter. |
| `string-drop` | `(string-drop string n)` | Everything but the first `n` characters. |
| `string-fields` | `(string-fields string)` | Splits on runs of whitespace, dropping the empty pieces. |
| `string-lines` | `(string-lines string)` | Splits on newlines, stripping a trailing `\r` from each line and dropping the final empty line that a trailing newline produces. |
| `string-titlecase` | `(string-titlecase string)` | Upper-cases the first letter of every word and lower-cases the rest. |
| `string-upper` | `(string-upper string)` | Unicode-aware upper case.  R7RS puts `string-upcase` in `(scheme char)`; this is the same job without that import. |
| `string-lower` | `(string-lower string)` | Unicode-aware lower case. |
| `string-blank?` | `(string-blank? string)` | `#t` for the empty string and for one that is all whitespace. |
| `string-empty?` | `(string-empty? string)` | `#t` for the empty string only. |
| `string-chomp` | `(string-chomp string)` | Removes one trailing newline, CRLF included. |
| `string-integer?` | `(string-integer? string [radix])` | Whether the **whole** string is an integer literal in `radix` (default 10, 2 to 36).  Useful where `string->number` would answer with a value. |
| `string-byte-length` | `(string-byte-length string)` | The UTF-8 length in bytes, where `string-length` counts characters. |

## Bytes, hashing and encodings

The hash procedures accept a string (its UTF-8 bytes) or a bytevector, and
return lowercase hexadecimal.  `md5` and `sha1` are here for protocols that
still ask for them, not for new designs.

| Procedure | Arguments | Description |
|---|---|---|
| `sha256` | `(sha256 data)` | SHA-256, as lowercase hex. |
| `sha1` | `(sha1 data)` | SHA-1, as lowercase hex. |
| `sha512` | `(sha512 data)` | SHA-512, as lowercase hex. |
| `md5` | `(md5 data)` | MD5, as lowercase hex. |
| `hmac-sha256` | `(hmac-sha256 key message)` | The keyed hash, as lowercase hex. |
| `crc32` | `(crc32 data)` | The IEEE checksum as an exact integer. |
| `random-bytes` | `(random-bytes count)` | A bytevector of `count` bytes from the system's cryptographic random source. |
| `hex-encode` | `(hex-encode bytevector)` | Lowercase hex. |
| `hex-decode` | `(hex-decode string)` | The bytevector a hex string stands for; invalid input is an error. |
| `base64-encode` | `(base64-encode bytevector)` | Standard base64 with padding. |
| `base64-decode` | `(base64-decode string)` | The decoded bytevector. |
| `base64url-encode` | `(base64url-encode bytevector)` | The URL-safe alphabet, padded. |
| `base64url-decode` | `(base64url-decode string)` | Decodes the padded and the raw form. |
| `base32-encode` | `(base32-encode bytevector)` | Standard base32, upper case. |
| `base32-decode` | `(base32-decode string)` | Decodes either case. |
| `bytes-xor` | `(bytes-xor a b)` | XORs `a` with `b`, cycling `b` when it is shorter: a one-byte `b` is a mask, a long one a key.  An empty `b` is an error. |
| `bytes-and` | `(bytes-and a b)` | Element-wise AND; the operands must have the same length. |
| `bytes-or` | `(bytes-or a b)` | Element-wise OR; the operands must have the same length. |
| `bytes-not` | `(bytes-not bytevector)` | Element-wise complement. |
| `bytes-reverse` | `(bytes-reverse bytevector)` | A new reversed bytevector. |
| `bytes-index` | `(bytes-index bytevector subsequence)` | The first index where the subsequence starts, or `#f`; the empty subsequence is at 0. |
| `bytevector-fill!` | `(bytevector-fill! bytevector byte [start [end]])` | Writes one byte through a range, in place, and returns the bytevector.  A range outside the bytevector is an error. |
| `vector->bytevector` | `(vector->bytevector vector)` | A bytevector from a vector of exact integers in `0..255`; anything else is an error. |
| `bytevector->vector` | `(bytevector->vector bytevector)` | A vector of exact integers, the way back into the vector batch lane. |

## Randomness

These use Go's `math/rand/v2` generator, which is seeded automatically and safe
to call from several interpreter threads; `random-bytes` and `uuid` use the
system's cryptographic source instead.

| Procedure | Arguments | Description |
|---|---|---|
| `random-int` | `(random-int n)` | A uniform exact integer in `[0, n)`; `n` must be at least 1. |
| `random-float` | `(random-float)` | A uniform inexact number in `[0, 1)`. |
| `random-string` | `(random-string length [alphabet])` | A uniform string over the alphabet, which defaults to the alphanumerics; an empty alphabet is an error. |
| `random-choice` | `(random-choice sequence)` | One element at random; an empty sequence is an error. |
| `shuffle` | `(shuffle list)` | A new list in random order; the original is untouched. |
| `vector-shuffle!` | `(vector-shuffle! vector)` | Shuffles in place and returns the vector. |
| `vector-sample` | `(vector-sample vector n)` | A new vector of `n` distinct elements chosen at random; `n` above the length is an error. |
| `uuid` | `(uuid)` | A random version-4 UUID in the usual 36-character textual form, from the cryptographic source. |

## Notes

* The `!` suffix means "in place": `sort!`, `vector-reverse!`,
  `vector-swap!`, `vector-filter!`, `vector-shuffle!` and the eight batch
  arithmetic variants write into the vector they were given and return it
  (`vector-swap!` returns an unspecified value).  Everything else builds a new
  sequence.
* `any` and `every` are SRFI-1's: they return the predicate's own value (the
  last one for `every`) rather than a boolean, and they accept several lists.
  A predicate written in Scheme is called once per element, so only a builtin
  one keeps the loop in Go.
* The names avoid the R7RS-small ones, so importing `(goscheme fast)` together
  with `(scheme base)` or `(scheme char)` never clashes.
* Pure Scheme data is not synchronized in this implementation; the randomness
  procedures are the only ones here that are safe to call from several
  interpreter threads at once.

## Example

```scheme
(import (scheme base) (scheme write) (goscheme fast))

;; One Go pass over the whole vector, and an in-place variant that allocates
;; nothing.
(define v (vector-iota 6))
(vector-scale! v 2)
(write v)                                     ; => #(0 2 4 6 8 10)
(write (vector-prefix-sum #(1 2 3)))          ; => #(1 3 6)

;; A builtin predicate runs inside the Go loop; a written one is called per
;; element.
(write (filter even? (iota 10)))              ; => (0 2 4 6 8)

;; sort-by computes its key once per element.
(write (sort-by string-length '("ccc" "a" "bb")))  ; => ("a" "bb" "ccc")

;; The two values of a partition: the reordered vector and the count.
(call-with-values (lambda () (vector-partition even? #(1 2 3 4)))
  (lambda (vec kept) (write vec) (write kept)))   ; => #(2 4 1 3)2

;; Number theory, strings and hashing.
(write (factor 1234567890))  ; => (2 3 3 5 3607 3803)
(write (string-split "a:b:c" ":" 2))          ; => ("a" "b:c")
(write (expt-mod 2 1000 1000000007))          ; => 688423210
(newline)
```
