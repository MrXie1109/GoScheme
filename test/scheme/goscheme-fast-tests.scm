;;; SPDX-License-Identifier: MIT
;;; The performance library: jobs done in Go rather than in interpreted Scheme.

(import (scheme base) (scheme write) (scheme char) (chibi test)
        (goscheme fast))

(test-begin "Fast")

;; ---------------------------------------------------------------------- order
(test-begin "Ordering")

(test '(1 2 3) (sort (list 3 1 2)))
(test '() (sort '()))
(test '(1) (sort (list 1)))
(test '(3 2 1) (sort (list 3 1 2) >))
(test '("a" "b" "c") (sort (list "b" "c" "a") string<?))
(test '(1 2 3) (sort (list 3 1 2) (lambda (a b) (< a b))))
;; A comparison the library does not know still works, through a Scheme call.
(test '(3 2 1) (sort (list 1 3 2) (lambda (a b) (> a b))))
(test '(1 2 3) (sort (list 2 3 1) <=))

(test #(1 2 3) (vector-sort (vector 3 1 2)))
(test #() (vector-sort (vector)))
(define sortable (vector 3 1 2))
(test #t (eq? sortable (sort! sortable)))
(test #(1 2 3) sortable)

(test 2 (vector-binary-search (vector 1 3 5 7) 5))
(test 0 (vector-binary-search (vector 1 3 5 7) 1))
(test 3 (vector-binary-search (vector 1 3 5 7) 7))
(test #f (vector-binary-search (vector 1 3 5 7) 4))
(test 1 (vector-binary-search (vector "a" "b" "c") "b" string<?))
(test 'not-a-procedure (guard (e (#t 'not-a-procedure)) (sort (list 1) 5)))

(test-end)

;; ------------------------------------------------------------------ sequences
(test-begin "Sequences")

(test '() (iota 0))
(test '(0 1 2) (iota 3))
(test '(10 20 30) (iota 3 10 10))
(test '(5 4 3) (iota 3 5 -1))
(test '(0 1/2 1) (iota 3 0 1/2))

(test #(3 2 1) (vector-reverse (vector 1 2 3)))
(test #() (vector-reverse (vector)))
(define v (vector 1 2 3))
(test #(3 2 1) (vector-reverse! v))
(define swapme (vector 1 2 3))
(vector-swap! swapme 0 2)
(test #(3 2 1) swapme)
(test 'out-of-range (guard (e (#t 'out-of-range)) (vector-swap! swapme 0 9)))

(test-end)

;; ------------------------------------------------------------------ selection
(test-begin "Selection")

(test '(0 2 4 6 8) (filter even? (iota 10)))
(test '() (filter even? '()))
(test '(6 7 8 9) (filter (lambda (x) (> x 5)) (iota 10)))
(test '("a" "b") (filter string? (list 1 "a" 'b "b" 2)))
(test '(2 4) (filter (lambda (x) (even? x)) (vector 1 2 3 4 5)))
(test 5 (count even? (iota 10)))
(test 0 (count even? '()))
(test 2 (count (lambda (x) (> x 7)) (iota 10)))
(test #f (any even? (list 1 3 5)))
(test #t (any even? (list 1 2 3)))
(test #t (every even? (list 2 4)))
(test #f (every even? (list 2 3)))
(test #t (every even? '()))
(define vf (vector 1 2 3 4 5 6))
(test 3 (vector-filter! even? vf))
(test 2 (vector-ref vf 0))
(test 4 (vector-ref vf 1))

(test-end)

;; ----------------------------------------------------------------- aggregates
(test-begin "Aggregates")

(test 0 (sum '()))
(test 55 (sum (iota 11)))
(test 6 (sum (vector 1 2 3)))
(test 3.5 (sum (list 1 2.5)))
(test 24 (product (list 1 2 3 4)))
(test 1 (product '()))
(test 1 (min-of (list 3 1 2)))
(test 3 (max-of (list 3 1 2)))
(test 1 (min-of (vector 3 1 2)))
(test 1/3 (min-of (list 1/3 1/2)))
(test 'empty (guard (e (#t 'empty)) (min-of '())))
(test 'empty (guard (e (#t 'empty)) (max-of (vector))))
(test 32 (vector-dot (vector 1 2 3) (vector 4 5 6)))
(test 0 (vector-dot (vector) (vector)))
(test 'lengths (guard (e (#t 'lengths)) (vector-dot (vector 1) (vector 1 2))))
(test 'not-a-number (guard (e (#t 'not-a-number)) (sum (list 1 'x))))

(test-end)

;; -------------------------------------------------------------------- strings
(test-begin "Strings")

(test '("a" "b" "c") (string-split "a,b,c" ","))
(test '("abc") (string-split "abc" ","))
;; With no separator the string is split into characters.
(test '("a" " " "b") (string-split "a b"))
(test '("a" "b" "c") (string-split "abc" ""))
(test "a-b" (string-join (list "a" "b") "-"))
(test "ab" (string-join (list "a" "b")))
(test "" (string-join '() "-"))
(test 6 (string-contains "hello world" "world"))
(test #f (string-contains "hello" "zz"))
(test 0 (string-contains "hello" ""))
(test 2 (string-index "hello" #\l))
(test #f (string-index "hello" #\z))
(test #t (string-prefix? "hello" "he"))
(test #f (string-prefix? "hello" "lo"))
(test #t (string-suffix? "hello" "lo"))
(test "x" (string-trim "  x  "))
(test "a" (string-trim "xxaxx" "x"))
(test "x  " (string-trim-left "  x  "))
(test "  x" (string-trim-right "  x  "))
(test "a+b+c" (string-replace "a-b-c" "-" "+"))
(test "abc" (string-replace "abc" "z" "+"))
(test "007" (string-pad-left "7" 3 #\0))
(test "7  " (string-pad-right "7" 3))
(test "777" (string-pad-left "777" 1))
(test 'empty-needle (guard (e (#t 'empty-needle)) (string-replace "abc" "" "x")))

(test-end)

;; ---------------------------------------------------------------------- bytes
(test-begin "Bytes")

;; The digest of "abc" is a fixed value, so this checks the hash itself.
(test "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" (sha256 "abc"))
(test "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" (sha256 ""))
(test (sha256 "abc") (sha256 (string->utf8 "abc")))
(test 'bad-hash (guard (e (#t 'bad-hash)) (sha256 5)))
(test "01ff" (hex-encode #u8(1 255)))
(test #u8(1 255) (hex-decode "01ff"))
(test #u8(1 255) (hex-decode (hex-encode #u8(1 255))))
(test 'bad-hex (guard (e (#t 'bad-hex)) (hex-decode "zz")))
(test "aGk=" (base64-encode #u8(104 105)))
(test #u8(104 105) (base64-decode "aGk="))
(test #u8(104 105) (base64-decode (base64-encode #u8(104 105))))
(test 'bad-base64 (guard (e (#t 'bad-base64)) (base64-decode "!!!")))
(test 8 (bytevector-length (random-bytes 8)))
(test 0 (bytevector-length (random-bytes 0)))

(test-end)

(test-end)
