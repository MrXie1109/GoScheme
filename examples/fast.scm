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

(newline)
(display "fast: end of tour") (newline)
