;;; SPDX-License-Identifier: MIT
;;; Data extensions: JSON, regular expressions and time.

(import (scheme base) (scheme write) (scheme char) (chibi test)
        (goscheme json) (goscheme regexp) (goscheme time) (goscheme hash-table))

(test-begin "Data")

;; ---------------------------------------------------------------------- json
(test-begin "JSON")

;; Scalars map onto the Scheme types a program already knows.
(test 42 (json-parse "42"))
(test #t (exact? (json-parse "42")))
(test 1.5 (json-parse "1.5"))
(test #t (inexact? (json-parse "1.5")))
(test "hi" (json-parse "\"hi\""))
(test #t (json-parse "true"))
(test #f (json-parse "false"))
(test 'null (json-parse "null"))

;; Arrays are vectors.
(test #(1 2 3) (json-parse "[1, 2, 3]"))
(test #() (json-parse "[]"))

;; Objects are hash tables with string keys.
(define obj (json-parse "{\"a\": 1, \"b\": \"two\"}"))
(test #t (hashtable? obj))
(test 1 (hash-table-ref/default obj "a" #f))
(test "two" (hash-table-ref/default obj "b" #f))
(test #f (hash-table-ref/default obj "missing" #f))

;; null is the symbol null in both directions, as in Racket.
(test "null" (json-write 'null))

;; Round trips of the scalar types.
(define (round-trip v) (json-parse (json-write v)))
(test 42 (round-trip 42))
(test -7 (round-trip -7))
(test 1.5 (round-trip 1.5))
(test "héllo 世界 \"quoted\" \\ backslash"
      (round-trip "héllo 世界 \"quoted\" \\ backslash"))
(test 'null (round-trip 'null))
(test #t (round-trip #t))
(test #f (round-trip #f))

;; Vectors and lists both write as JSON arrays; reading back gives a vector,
;; so an array round trip is value-preserving rather than representation-
;; preserving for a list.
(test #(1 2 3) (round-trip #(1 2 3)))
(test #(1 2 3) (round-trip '(1 2 3)))
(test #(#t #f null) (round-trip (list #t #f 'null)))

;; A big integer stays exact and keeps every digit across the round trip.
(define big 123456789012345678901234567890)
(test #t (exact? big))
(test big (json-parse "123456789012345678901234567890"))
(test big (round-trip big))

;; Nested documents: parse, inspect, write canonically, parse again.
(define nested
  (json-parse "{\"a\": [1, 2, {\"b\": null}], \"c\": true, \"d\": {\"e\": \"f\"}}"))
(test #t (hashtable? nested))
(test #t (vector? (hash-table-ref/default nested "a" #f)))
(test 2 (vector-ref (hash-table-ref/default nested "a" #f) 1))
(test 'null
      (hash-table-ref/default (vector-ref (hash-table-ref/default nested "a" #f) 2) "b" #f))
(test #t (hash-table-ref/default nested "c" #f))
(test "f" (hash-table-ref/default (hash-table-ref/default nested "d" #f) "e" #f))
(test "{\"a\":[1,2,{\"b\":null}],\"c\":true,\"d\":{\"e\":\"f\"}}"
      (json-write nested))

(define nested2 (json-parse (json-write nested)))
(test 2 (vector-ref (hash-table-ref/default nested2 "a" #f) 1))
(test 'null
      (hash-table-ref/default (vector-ref (hash-table-ref/default nested2 "a" #f) 2) "b" #f))

;; Malformed input and values with no JSON representation raise.
(test-error (json-parse ""))
(test-error (json-parse "{"))
(test-error (json-parse "[1, 2"))
(test-error (json-parse "nope"))
(test-error (json-parse "1 2"))
(test-error (json-write 'hello))
(test-error (json-write (lambda (x) x)))
(define bad-keys (make-equal-hashtable))
(hash-table-set! bad-keys 1 2)
(test-error (json-write bad-keys))

(test-end)

;; -------------------------------------------------------------------- regexp
(test-begin "Regexp")

(define re (regexp "([a-z]+)([0-9]+)"))
(test #t (regexp? re))
(test #f (regexp? "abc"))
(test #t (regexp? (regexp "x")))

;; Element 0 is the whole match, then the capture groups.
(test '("abc123" "abc" "123") (regexp-match re "abc123yy"))
(test '("abc123" "abc" "123") (regexp-match "([a-z]+)([0-9]+)" "abc123yy"))
(test #f (regexp-match re "nope"))
(test #t (regexp-match? re "abc123yy"))
(test #t (regexp-match? "abc" "xxabc"))
(test #f (regexp-match? re "nope"))

;; Positions are (start . end) byte offsets, half-open.
(test '((0 . 6) (0 . 3) (3 . 6)) (regexp-match-positions re "abc123yy"))
(test '((0 . 3)) (regexp-match-positions "abc" "abcdef"))
(test #f (regexp-match-positions re "nope"))

;; Replacements expand $1, $2, ... from the capture groups.
(test "abc-123" (regexp-replace "([a-z]+)([0-9]+)" "abc123" "$1-$2"))
(test "123-abc" (regexp-replace "([a-z]+)([0-9]+)" "abc123" "$2-$1"))
(test "abc" (regexp-replace "z" "abc" "!"))
(test "[123]-[abc]" (regexp-replace-all "([a-z]+)([0-9]+)" "abc123" "[$2]-[$1]"))
(test "YY123YY" (regexp-replace-all "[a-z]+" "abc123def" "YY"))

(test '("a" "b" "c") (regexp-split "," "a,b,c"))
(test '("a" "b" "c") (regexp-split "[0-9]+" "a1b22c"))

;; A pattern that RE2 rejects is an error, whether compiled eagerly or on use.
(test-error (regexp "("))
(test-error (regexp-match "(" "x"))

(test-end)

;; ---------------------------------------------------------------------- time
(test-begin "Time")

(test #t (exact? (current-millisecond)))
(test #t (> (current-millisecond) 1600000000000))

;; sleep accepts zero, and a short sleep advances the monotonic clock.
(sleep 0)
(define t0 (monotonic-millisecond))
(sleep 20)
(define t1 (monotonic-millisecond))
(test #t (exact? t1))
(test #t (>= t1 t0))
(test #t (>= (- t1 t0) 10))

;; Formatting and parsing share Go's reference layout and work in UTC.
(define stamp 1700000000123)          ; 2023-11-14T22:13:20.123Z
(test "2023-11-14 22:13:20" (time-format stamp "2006-01-02 15:04:05"))
(test "2023-11-14 22:13:20.123" (time-format stamp "2006-01-02 15:04:05.000"))
(test 1700000000000 (time-parse "2023-11-14 22:13:20" "2006-01-02 15:04:05"))
(test stamp (time-parse "2023-11-14 22:13:20.123" "2006-01-02 15:04:05.000"))
(test #f (time-parse "not a date" "2006-01-02"))
(test #f (time-parse "2023-11-14" "2006-01-02 15:04:05"))

;; time-utc-parts is an alist keyed by symbols.
(define parts (time-utc-parts stamp))
(define (part k) (cdr (assq k parts)))
(test 7 (length parts))
(test 2023 (part 'year))
(test 11 (part 'month))
(test 14 (part 'day))
(test 22 (part 'hour))
(test 13 (part 'minute))
(test 20 (part 'second))
(test 2 (part 'weekday))              ; Tuesday, Sunday is 0
(test #t (exact? (part 'second)))

(test-end)

(test-end)
