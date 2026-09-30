;;; SPDX-License-Identifier: MIT
;;; SRFI-133, the vector library, with (goscheme fast) imported too, because
;;; the two share vector-reverse!, vector-swap!, vector-binary-search and
;;; vector-partition.

(import (scheme base) (scheme write) (chibi test)
        (srfi 133) (goscheme fast))

(test-begin "SRFI 133")

;; --------------------------------------------------------------- predicates
(test #t (vector-empty? (vector)))
(test #f (vector-empty? (vector 1)))
(test #t (vector= = (vector 1 2) (vector 1 2)))
(test #t (vector= = (vector)))
(test #t (vector= = (vector 1 2) (vector 1 2) (vector 1 2)))
(test #f (vector= = (vector 1 2) (vector 1 3)))
(test #f (vector= = (vector 1) (vector 1 2)))
(test #t (vector= string-ci=? (vector "a") (vector "A")))
(test 'not-a-vector (guard (e (#t 'not-a-vector)) (vector= = 5 (vector 1))))

;; -------------------------------------------------------------- selectors
(test #(2 3) (subvector (vector 1 2 3 4) 1 3))
(test #() (subvector (vector 1 2) 1 1))
(test 'bad-range (guard (e (#t 'bad-range)) (subvector (vector 1 2) 1 5)))
(test #(3 2 1) (reverse-list->vector '(1 2 3)))
(test #() (reverse-list->vector '()))

;; ------------------------------------------------------------ constructors
(test #(0 1 4 9) (vector-unfold (lambda (i) (* i i)) 4))
(test #() (vector-unfold (lambda (i) i) 0))
(test #(0 1 2 3) (vector-unfold (lambda (i x) (values x (+ x 1))) 4 0))
(test #(0 1 2) (vector-unfold-right (lambda (i) i) 3))
(test #(10 12 14)
      (call-with-values
        (lambda () (vector-unfold (lambda (i x y) (values (+ x y) (+ x 1) (+ y 1))) 3 0 10))
        (lambda (v) v)))
(test 'bad-unfold (guard (e (#t 'bad-unfold)) (vector-unfold (lambda (i) (values i i)) 2)))
(test #(3 2 1) (vector-reverse-copy (vector 1 2 3)))
(test #(3 2) (vector-reverse-copy (vector 1 2 3 4) 1 3))
(test #(1 2 3) (vector-concatenate (list (vector 1 2) (vector 3))))
(test #() (vector-concatenate '()))
(test #(1 2 8) (vector-append-subvectors (vector 1 2 3) 0 2 (vector 9 8) 1 2))
(test 'bad-subvectors (guard (e (#t 'bad-subvectors)) (vector-append-subvectors (vector 1) 0)))

;; ---------------------------------------------------------------- iteration
(test '(3 2 1) (vector-fold (lambda (acc x) (cons x acc)) '() (vector 1 2 3)))
(test 11 (vector-fold (lambda (acc x y) (+ acc (* x y))) 0 (vector 1 2) (vector 3 4)))
(test '(1 2 3) (vector-fold-right (lambda (x acc) (cons x acc)) '() (vector 1 2 3)))
(test 6 (vector-fold (lambda (acc x) (+ acc x)) 0 (vector 1 2 3)))
(test '(1 2) (vector-fold-right (lambda (x acc) (cons x acc)) '() (vector 1 2)))
(define mapped (vector 1 2 3))
(test #t (eq? mapped (vector-map! (lambda (x) (* x 10)) mapped)))
(test #(10 20 30) mapped)
(test 2 (vector-count even? (vector 1 2 3 4)))
(test 0 (vector-count even? (vector)))
(test 1 (vector-count < (vector 1 5) (vector 3 2)))

;; ---------------------------------------------------------------- searching
(test 1 (vector-index even? (vector 1 2 3 4)))
(test #f (vector-index even? (vector 1 3)))
(test 3 (vector-index-right even? (vector 1 2 3 4)))
(test 2 (vector-skip even? (vector 2 4 5 6)))
(test 2 (vector-skip-right even? (vector 2 4 5 6)))
(test #f (vector-skip even? (vector 2 4)))
(test 0 (vector-index < (vector 1 5) (vector 3 2)))
(test #t (vector-any even? (vector 1 3 4)))
(test #f (vector-any even? (vector 1 3)))
(test 20 (vector-any (lambda (x) (and (even? x) (* x 10))) (vector 1 2 3)))
(test #t (vector-every even? (vector 2 4)))
(test #f (vector-every even? (vector 2 3)))
(test #t (vector-every even? (vector)))
(test 40 (vector-every (lambda (x) (and (even? x) (* x 10))) (vector 2 4)))
(test #t (vector-any < (vector 1 5) (vector 3 2)))
(call-with-values (lambda () (vector-partition even? (vector 1 2 3 4)))
  (lambda (vec kept) (test #(2 4 1 3) vec) (test 2 kept)))
(call-with-values (lambda () (vector-partition even? (vector 1 3)))
  (lambda (vec kept) (test #(1 3) vec) (test 0 kept)))

;; The shared binding takes SRFI-133's three-valued comparison and the fast
;; library's predicate, and finds the first of several equal elements.
(test 2 (vector-binary-search (vector 1 3 5 7) 5
          (lambda (a b) (cond ((< a b) -1) ((> a b) 1) (else 0)))))
(test #f (vector-binary-search (vector 1 3 5 7) 4
           (lambda (a b) (cond ((< a b) -1) ((> a b) 1) (else 0)))))
(test 2 (vector-binary-search (vector 1 3 5 7) 5))
(test #f (vector-binary-search (vector 1 3 5 7) 4))
(test 1 (vector-binary-search (vector 1 3 3 3 5) 3))

;; ------------------------------------------------------------------ mutators
(define rv (vector 1 2 3 4))
(test #t (eq? rv (vector-reverse! rv 1 3)))
(test #(1 3 2 4) rv)
(define rv2 (vector 1 2 3))
(vector-reverse! rv2)
(test #(3 2 1) rv2)
(define sv (vector 1 2 3))
(vector-swap! sv 0 2)
(test #(3 2 1) sv)

;; ------------------------------------------------- the R7RS names it shares
(test #(1 2) (vector-copy (vector 1 2 3) 0 2))
(test #(9 9 9) (let ((v (vector 1 2 3))) (vector-fill! v 9) v))
(test #(2 4 6) (vector-map (lambda (x) (* 2 x)) (vector 1 2 3)))
(test '(1 2 3) (let ((acc '())) (vector-for-each (lambda (x) (set! acc (cons x acc))) (vector 1 2 3)) (reverse acc)))
(test '(1 2) (vector->list (vector 1 2)))
(test #(1 2) (list->vector '(1 2)))
(test #(1 2 3) (vector-append (vector 1) (vector 2 3)))
(test #(#\h #\i) (string->vector "hi"))
(test "hi" (vector->string (vector #\h #\i)))

(test-end)
