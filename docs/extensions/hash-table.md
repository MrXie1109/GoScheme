# (goscheme hash-table)

This library provides mutable hash tables (associative maps) for GoScheme. It follows
SRFI 69 and SRFI 125 / R7RS-large: three constructors choose between `eq?`, `eqv?` and
`equal?` key equivalence, and a content-based hash function backs them. It is an
extension library, not part of R7RS-small:

```scheme
(import (scheme base) (goscheme hash-table))
```

## Procedures

### Constructors and predicates

| Procedure | Arguments | Description |
|---|---|---|
| `make-eq-hashtable` | `([size])` | Returns a new empty mutable table whose keys are compared with eq?. The optional size is accepted for SRFI 69 compatibility but is ignored: it is neither a capacity hint nor a size limit. |
| `make-eqv-hashtable` | `([size])` | Returns a new empty mutable table whose keys are compared with eqv?. The ignored size argument behaves as above. Numbers are compared by value and exactness, so 1 and 1.0 are different keys. |
| `make-equal-hashtable` | `([size])` | Returns a new empty mutable table whose keys are compared with equal?. The ignored size argument behaves as above. Strings, bytevectors, pairs and vectors are compared by content. This is the default table kind used elsewhere in the library. |
| `make-hash-table` | `([size-hint [equivalence]])` | Returns a new empty mutable table. An exact integer is a size hint, which this implementation accepts and does not need; a symbol or procedure naming eq?, eqv? or equal? selects the comparison, so `(make-hash-table 'eqv)` is an eqv? table and the default is equal?. Anything else raises instead of being ignored. This is the SRFI 69 style constructor name and the one JSON code paths use. |
| `hashtable?` | `(obj)` | Returns #t when obj is a hash table, otherwise #f. Equivalent to the R7RS-large spelling below. |
| `hash-table?` | `(obj)` | Returns #t when obj is a hash table, otherwise #f. Equivalent to the SRFI 69 spelling above. |

### Lookup

| Procedure | Arguments | Description |
|---|---|---|
| `hash-table-ref` | `(table key [failure])` | Returns the value associated with key. When key is absent and failure is supplied, failure is called with zero arguments and its result is returned. When key is absent and no failure is given, raises an error of the form "hash-table-ref: no value associated with key <written key>". The failure argument is not type-checked unless it is actually applied. |
| `hash-table-ref/default` | `(table key default)` | Returns the value associated with key, or default when the key is absent. default is any object and is returned as is; it is never called. A stored #f is returned as #f, not replaced by default. |
| `hash-table-contains?` | `(table key)` | Returns #t when the table has a live association for key, otherwise #f. It is a pure predicate: a key mapped to #f still counts as present. Identical in behavior to hash-table-exists?. |
| `hash-table-exists?` | `(table key)` | Returns #t when the table has a live association for key, otherwise #f. Identical in behavior to hash-table-contains?; the two names differ only in spelling. |
| `hash-table-count` | `(table)` | Returns the number of live associations as an exact integer. It is a synonym for hash-table-size, not a capacity. |
| `hash-table-size` | `(table)` | Returns the number of live associations as an exact integer. It is a synonym for hash-table-count: the two are separate procedures with identical behavior, and there is no separate notion of table capacity. |

### Mutation

| Procedure | Arguments | Description |
|---|---|---|
| `hash-table-set!` | `(table key value)` | Associates key with value, adding a new association or replacing the existing one (the table is not enlarged when the key is already present). Returns an unspecified value. |
| `hash-table-delete!` | `(table key)` | Removes the association for key when present. Returns an unspecified value whether or not the key was present, so it cannot be used as a presence test; use hash-table-contains? or hash-table-exists? and then hash-table-count to observe the effect. |
| `hash-table-update!` | `(table key proc [failure])` | Stores (proc old) under key, where old is the current value if the key is present, the failure object if the key is absent and a fourth argument was supplied, and an unspecified value if the key is absent and no failure was supplied. proc must accept one argument. The optional failure is passed to proc as an ordinary value and is not called; this differs from SRFI 69, whose failure argument is a thunk. Returns an unspecified value. |
| `hash-table-clear!` | `(table)` | Removes every live association, leaving the table empty and still usable (its key equivalence is unchanged). Returns an unspecified value. |
| `hash-table-copy` | `(table)` | Returns a new mutable table with the same key equivalence and the same live associations. The copy is independent of the original for later updates, but keys and values are shared shallowly, so mutating a stored object is visible through both tables. |

### Iteration and conversion

| Procedure | Arguments | Description |
|---|---|---|
| `hash-table-walk` | `(table proc)` | Calls proc once per live association with two arguments, the key and the value, then returns an unspecified value. The live entries are snapshotted before iterating and are currently visited in insertion order, but no order is guaranteed; deleting entries during the walk is safe and does not skip entries, and entries added during the walk are not visited by that walk. |
| `hash-table-keys` | `(table)` | Returns a list of the live keys. The order is unspecified, though the current implementation returns insertion order. |
| `hash-table-values` | `(table)` | Returns a list of the live values, ordered to match hash-table-keys. The order is unspecified. |
| `hash-table->alist` | `(table)` | Returns a list of (key . value) pairs, one per live association, in the same order as the key and value lists. The pairs are freshly allocated. |
| `alist->hash-table` | `(alist [table])` | Adds the associations of alist, which must be a proper association list, to the given table and returns it; with no table it builds a fresh equal? one, as SRFI 69 does. Each element must be a pair; its car is the key and its whole cdr is the value (so the value of (a 1 2) is the list (1 2)). Later duplicate keys overwrite earlier ones, and a supplied table keeps its own comparison. |

### Hashing

| Procedure | Arguments | Description |
|---|---|---|
| `hash` | `(obj [bound])` | Returns a non-negative exact integer derived from obj by equal?-style content hashing, so objects that are equal? under this library's key comparison hash alike. With bound, bound must be an exact integer in the range 1 to 2^40; the result is an exact integer in [0, bound). A bound of 0, a negative bound, a non-integer bound or one above 2^40 raises an error. This procedure is not part of (scheme base), which has no hashing procedure; the base names it relates to are the equality predicates eq?, eqv? and equal?, which select the three table kinds and define when two keys hash alike. |

## Notes

* Type errors. Every procedure that takes a table except the constructors, the two
  predicates and the hash procedure raises an error of the form "<name>: expected a
  hash table" when given a non-table. hash-table-walk rejects a non-procedure second
  argument, and hash-table-update! rejects a non-procedure third argument, with
  "expected a procedure but got ..." messages.
* Missing keys. hash-table-ref raises when the key is absent and no failure argument
  was supplied. Use hash-table-ref/default for a plain fallback value, or pass a thunk
  as the failure argument when the fallback must be computed. A key explicitly mapped
  to #f is present, so neither form treats it as missing.
* alist->hash-table errors. It raises when its first argument is not a proper list,
  when an element is not a pair, or when the optional second argument is not a hash
  table. With no second argument the result uses equal? comparison; when one is given
  the table's own comparison applies.
* Update semantics. hash-table-update! reads the old value, applies the procedure, and
  stores the result; the table is not otherwise synchronized, and the procedure runs
  with the table in its prior state. The fourth argument is a value, not a thunk, so
  code written for SRFI 69 that passes a thunk must call it inside the update
  procedure instead.
* Equality semantics. eq? keys are compared by identity, with the interpreter's eq?
  treating symbols by name and small exact numbers, characters, booleans and the
  empty list by value. eqv? keys add numbers compared by value and exactness (1 and
  1.0 are distinct, and +nan.0 is not eqv? to itself), plus characters. equal? keys
  compare strings, bytevectors, pairs, vectors and error objects by content and
  records by identity; the content hash used for equal? tables detects cycles, so
  cyclic keys do not hang. Symbols hash and compare by name in all three kinds.
* No capacity control. The optional size argument of the constructors is ignored, and
  the table-size procedures report the number of associations rather than any
  allocated capacity. Tables grow as needed.
* Thread safety. Hash tables are ordinary Scheme data and are not synchronized. The
  interpreter's own shared state (the global environment, ports and parameters) may be
  shared between interpreter threads, but pairs, strings, vectors, records and hash
  tables may not be mutated concurrently. Sharing one table between (go ...) threads
  without external locking is a data race; guard it with a (goscheme sync) mutex, or
  share memory by communicating as the concurrency notes recommend.
* Iteration order. The implementation currently visits entries in insertion order,
  including after deletions, but this is an implementation detail and not a documented
  guarantee; hash-table-walk, hash-table-keys, hash-table-values and
  hash-table->alist should be treated as unordered by portable code.

## Example

```scheme
(import (scheme base) (scheme write) (goscheme hash-table))

;; eq? tables compare keys by identity, equal? tables by content.
(define eq-table (make-eq-hashtable))
(define equal-table (make-equal-hashtable))
(define key (string-copy "color"))
(hash-table-set! eq-table key 'red)
(hash-table-set! equal-table key 'red)
(display (hash-table-ref/default eq-table (string-copy "color") 'absent))
(newline)                                     ; absent
(display (hash-table-ref/default equal-table (string-copy "color") 'absent))
(newline)                                     ; red

;; ref raises when the key is missing; the optional failure thunk is called
;; with no arguments.  ref/default returns a plain fallback instead.
(display (hash-table-ref equal-table (string-copy "color")))
(newline)                                     ; red
(display (hash-table-ref equal-table 'missing (lambda () 'fallback)))
(newline)                                     ; fallback
(display (hash-table-ref/default equal-table 'missing 'default))
(newline)                                     ; default
(display (guard (e (#t (error-object-message e)))
           (hash-table-ref equal-table 'missing)))
(newline)                                     ; hash-table-ref: no value associated with key missing

;; set!, delete!, update! and clear! all return an unspecified value.
(define t (make-equal-hashtable))
(hash-table-set! t 'n 1)
(hash-table-update! t 'n (lambda (v) (+ v 41)))
(hash-table-update! t 'k (lambda (v) (list 'was v)) 'none)
(display (hash-table-ref t 'n))
(newline)                                     ; 42
(display (hash-table-ref t 'k))
(newline)                                     ; (was none)
(hash-table-delete! t 'k)
(display (hash-table-count t))
(newline)                                     ; 1
(display (hash-table-count (hash-table-copy t)))
(newline)                                     ; 1

;; Iteration is in insertion order here, though the library does not promise it.
(define order (make-equal-hashtable))
(for-each (lambda (k) (hash-table-set! order k (* k k))) '(3 1 2))
(display (hash-table-keys order))
(newline)                                     ; (3 1 2)
(display (hash-table-values order))
(newline)                                     ; (9 1 4)
(display (hash-table->alist order))
(newline)                                     ; ((3 . 9) (1 . 1) (2 . 4))
(let ((acc '()))
  (hash-table-walk order (lambda (k v) (set! acc (cons (list k v) acc))))
  (display (reverse acc))
  (newline))                                  ; ((3 9) (1 1) (2 4))

;; alist->hash-table fills the table it is given, or builds a fresh equal? one.
(display (hash-table->alist (alist->hash-table '((a . 1) (b . 2)))))
(newline)                                     ; ((a . 1) (b . 2))

;; hash is not part of (scheme base); it hashes with equal? semantics and,
;; given a bound, returns an exact integer in [0, bound).
(display (= (hash (list 1 2)) (hash (list 1 2))))
(newline)                                     ; #t
(display (let ((x (hash "abc" 16))) (and (<= 0 x) (< x 16))))
(newline)                                     ; #t
```
