#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; (goscheme fast): the jobs Scheme does slowly, done in Go.
;;;
;;; Run with:  goscheme examples/fast.scm
;;;
;;; The library exists for two reasons.  One is doing the loop, the indexing and
;;; the copying natively instead of as interpreted steps.  The other, and often
;;; the bigger one, is not calling back into Scheme for every element: a sort or
;;; a filter whose predicate is a builtin runs entirely in Go.
;;;
;;; The first half of this file measures that difference on this machine, and
;;; the second half shows the rest of the library.

(import (scheme base) (scheme write)
        (goscheme fast)
        (goscheme time))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

(define (timed thunk)
  (let ((start (monotonic-millisecond)))
    (let ((value (thunk)))
      (list value (- (monotonic-millisecond) start)))))

;; The measured lines read better as plain numbers than as written values.
(define (report what scheme-card go-card)
  (let ((scheme-ms (cadr scheme-card))
        (go-ms (cadr go-card)))
    (display what)
    (display " -- Scheme: ") (display scheme-ms) (display " ms")
    (display ", (goscheme fast): ") (display go-ms) (display " ms")
    (if (> go-ms 0)
        (begin (display "  (") (display (quotient scheme-ms go-ms)) (display "x)")))
    (newline)))

;;; ------------------------------------------------------- sorting, measured
(define (merge a b less?)
  (cond ((null? a) b)
        ((null? b) a)
        ((less? (car b) (car a)) (cons (car b) (merge a (cdr b) less?)))
        (else (cons (car a) (merge (cdr a) b less?)))))

(define (split l)
  ;; Two halves, returned as two values: the tortoise gives the start of the
  ;; second half when the hare runs out.
  (let loop ((slow l) (fast l) (acc '()))
    (if (or (null? fast) (null? (cdr fast)))
        (values (reverse acc) slow)
        (loop (cdr slow) (cddr fast) (cons (car slow) acc)))))

(define (merge-sort l less?)
  (if (or (null? l) (null? (cdr l)))
      l
      (call-with-values (lambda () (split l))
        (lambda (left right)
          (merge (merge-sort left less?) (merge-sort right less?) less?)))))

;; A shuffled list to sort, built once and used by both versions.
(define data
  (let loop ((i 0) (acc '()))
    (if (= i 2000) acc (loop (+ i 1) (cons (modulo (* i 7919) 2000) acc)))))

(define in-scheme (timed (lambda () (length (merge-sort data <)))))
(define in-go (timed (lambda () (length (sort data <)))))
(report "sorting 2000 numbers" in-scheme in-go)
(show "  both sorted the same number of elements:" (= (car in-scheme) (car in-go)))

;;; ------------------------------------------------------ filtering, measured
(define numbers (iota 100000))
(define filter-scheme
  (timed (lambda ()
           (length (let loop ((l numbers) (acc '()))
                     (cond ((null? l) acc)
                           ((even? (car l)) (loop (cdr l) (cons (car l) acc)))
                           (else (loop (cdr l) acc))))))))
(define filter-go (timed (lambda () (length (filter even? numbers)))))
(report "filtering 100000 numbers" filter-scheme filter-go)

;;; --------------------------------------------- searching a string, measured
(define haystack (make-string 50000 #\x))
(define (find-in-scheme s sub)
  (let ((n (string-length s)) (m (string-length sub)))
    (let loop ((i 0))
      (cond ((> (+ i m) n) #f)
            ((string=? (substring s i (+ i m)) sub) i)
            (else (loop (+ i 1)))))))
(define search-scheme (timed (lambda () (find-in-scheme haystack "xxxxy"))))
(define search-go (timed (lambda () (string-contains haystack "xxxxy"))))
(report "searching a 50000 character string" search-scheme search-go)

;;; ------------------------------------------- batch arithmetic, measured
;;; One Go pass over the whole vector, against the same loop written out one
;;; element at a time.  There is no SIMD here, but the per-element type checks,
;;; the boxing and the interpreted steps all disappear.
(define wide-a (vector-iota 200000))
(define wide-b (vector-iota 200000 1))
(define (add-in-scheme a b)
  (let* ((n (vector-length a)) (out (make-vector n 0)))
    (let loop ((i 0))
      (if (= i n)
          out
          (begin (vector-set! out i (+ (vector-ref a i) (vector-ref b i)))
                 (loop (+ i 1)))))))
(define add-scheme (timed (lambda () (vector-ref (add-in-scheme wide-a wide-b) 199999))))
(define add-go (timed (lambda () (vector-ref (vector-add wide-a wide-b) 199999))))
(report "adding two 200000 element vectors" add-scheme add-go)
(show "  the same last element:" (= (car add-scheme) (car add-go)))

;;; --------------------------------------------------------- the rest of it
(show "iota:" (iota 5) (iota 3 10 10))
(show "aggregates:" (sum (iota 101)) (product '(1 2 3 4)) (min-of '(3 1 2)) (max-of '(3 1 2)))
(show "vector-dot:" (vector-dot #(1 2 3) #(4 5 6)))
(show "selection:" (filter odd? (iota 10)) (count even? (iota 10))
      (list (any even? '(1 3)) (every even? '(2 4))))
(show "ordering a vector:" (vector-sort #(3 1 2) >) (vector-binary-search #(1 3 5 7) 5))

(define csv "ada,36,mathematician\ngrace,45,admiral")
(define rows (map (lambda (row) (string-split row ",")) (string-split csv "\n")))
(show "a tiny CSV:" rows)
(show "joined back:" (string-join (list "a" "b" "c") "-"))
(show "trimmed and padded:" (list (string-trim "  x  ") (string-pad-left "7" 3 #\0)))

(show "sha256 of \"abc\":" (sha256 "abc"))
(show "encodings:" (hex-encode #u8(1 255)) (base64-encode #u8(104 105))
      (base64-decode "aGk="))
(show "a random token of 16 bytes:" (bytevector-length (random-bytes 16)))

;;; ------------------------------------------------------- lists and numbers
(show "lists:" (take 3 '(1 2 3 4)) (drop 3 '(1 2 3 4)) (last '(1 2 3))
      (flatten '(1 (2 #(3)))))
(show "select:" (list-index even? '(1 3 4)) (find even? '(1 3 4))
      (delete-duplicates '(1 2 1 3)))
(show "folds:" (fold-left + 0 '(1 2 3 4)) (fold-right - 0 '(1 2 3)))
(show "sort-by:" (sort-by string-length '("ccc" "a" "bb")))
(show "statistics:" (mean '(1 2 3)) (median '(1 2 3 4)) (stddev '(1 2 3))
      (mode '(1 2 2 3)))
(show "bits:" (bit-and 12 10) (bit-shift 1 20) (bit-count 255) (integer-length 1000))
(show "primes:" (prime? 1000003) (length (primes 100000)) (factor 1234567890))
(show "modular:" (expt-mod 2 1000 1000000007) (isqrt 1000000))

;;; ------------------------------------------------------------- batch vectors
(show "batch:" (vector-add #(1 2 3) #(10 20 30)) (vector-prefix-sum #(1 2 3))
      (vector-norm #(3 4)))
(show "batch, more:" (vector-argmax #(3 9 2)) (vector-clamp #(-1 5 15) 0 10)
      (vector-compare #(1 2) #(1 3)) (vector-binary-search-insert #(1 3 5) 4))
(define scratch (vector 1 2 3))
(vector-scale! scratch 2)
(vector-negate! scratch)
(show "in place:" scratch (vector-sample #(1 2 3 4 5) 2))

;;; ---------------------------------------------------------------- text, bytes
(show "text:" (string-reverse "abc") (string-count "banana" #\a)
      (string-fields " a  b ") (string-sort "caba"))
(show "text, more:" (string-lines "a\r\nb\r\n") (string-titlecase "hello WORLD")
      (string-chunk "abcdef" 3) (string-find-all "aaaa" "aa"))
(show "text, more still:" (string-integer? "-42") (string-pad-center "ab" 6 #\-)
      (string-replace-first "aaa" "a" "b") (string-byte-length "héllo"))
(show "hashing:" (sha1 "abc") (hmac-sha256 "key" "msg") (crc32 "abc"))
(show "encodings, more:" (base64url-encode #u8(251 255)) (base32-encode #u8(104 105))
      (bytes-xor #u8(1 2 3) #u8(255)))
(show "random:" (string-length (uuid)) (string-length (random-string 16))
      (< (random-int 100) 100) (length (shuffle '(1 2 3 4))))

(newline)
(display "fast: end of tour") (newline)
