;;; SPDX-License-Identifier: MIT
;;; Pattern matching: the (goscheme match) library.
;;;
;;; A clause is (pattern body ...), or (pattern (guard test) body ...) when the
;;; body needs a condition.  The guard is part of the clause, not of the
;;; pattern: ((n (guard #t)) 'big) is a pattern that matches a two-element list,
;;; which is why the tests below write (n (guard #t) 'big).

(import (scheme base) (scheme write) (scheme char) (chibi test) (goscheme match))

(test-begin "Match")

;; ------------------------------------------------------------------- literals
(test-begin "Literals")

(test 'five (match 5 (5 'five) (_ 'other)))
(test 'string (match "s" ("s" 'string) (_ 'other)))
(test 'char (match #\a (#\a 'char) (_ 'other)))
(test 'true (match #t (#t 'true) (_ 'other)))
(test 'false (match #f (#f 'false) (_ 'other)))
(test 'the-symbol (match 'x ('x 'the-symbol) (_ 'other)))
(test 'other (match 6 (5 'five) (_ 'other)))
(test 'empty (match '() (() 'empty) (_ 'other)))
(test 'one-element (match '(1) ((1) 'one-element) (_ 'other)))

;; A number that is written as a list is a list pattern, not a literal.
(test 'two-elements (match '(5 6) ((5 6) 'two-elements) (_ 'other)))

(test-end)

;; ------------------------------------------------------------------ variables
(test-begin "Variables")

(test 5 (match 5 (n n)))
(test '(1 2 3) (match '(1 2 3) (all all)))
(test 'bound (match 'anything (x 'bound)))

;; Every symbol except _ and else binds, including one that shadows an outer
;; binding.
(define shadowed 'outer)
(test 'inner (match 'inner (shadowed shadowed)))
(test 'outer shadowed)

;; The subject is evaluated once, however many clauses look at it.
(define evaluations 0)
(test 'one (match (begin (set! evaluations (+ evaluations 1)) 1)
             (1 'one)
             (2 'two)))
(test 1 evaluations)

(test-end)

;; ---------------------------------------------------------------------- lists
(test-begin "Lists")

(test '(1 2 3) (match '(1 2 3) ((a b c) (list a b c))))
(test '(1 (2 3)) (match '(1 2 3) ((a . rest) (list a rest))))
(test '(1 2) (match '(1 . 2) ((a . b) (list a b))))
(test 'nested (match '((1 2) (3 4)) (((a b) (c d)) (if (= (+ a b c d) 10) 'nested 'no))))
(test 'no (match '(1 2) ((a b c) 'three) (_ 'no)))
(test 'exact (match '(1 2) ((a b) 'exact) ((a b c) 'three)))

;; A pattern with several variables of the same name keeps the last binding.
(test 2 (match '(1 2) ((a a) a)))

(test-end)

;; -------------------------------------------------------------------- vectors
(test-begin "Vectors")

(test '(1 2) (match #(1 2) (#(a b) (list a b))))
(test 'three (match #(1 2 3) (#(a b) 'two) (#(a b c) 'three)))
(test 'no (match #(1 2) ((a b) 'list-not-vector) (_ 'no)))

(test-end)

;; --------------------------------------------------------------- combinators
(test-begin "Combinators")

(test 'small-odd (match 5 ((or 1 3 5 7) 'small-odd) (_ 'other)))
(test 'other (match 4 ((or 1 3 5 7) 'small-odd) (_ 'other)))
(test 7 (match 7 ((and (or 1 2 7) n) n)))
(test 'not-small (match 9 ((not (or 1 2)) 'not-small) (_ 'small)))
(test 'caught (match 2 ((and (or 1 2) n) 'caught) (_ 'other)))

(test-end)

;; -------------------------------------------------------------------- guards
(test-begin "Guards")

(define (size n)
  (match n
    (n (guard (> n 5)) 'big)
    (n (guard (> n 0)) 'small)
    (n (guard (= n 0)) 'zero)
    (else 'negative)))

(test 'big (size 7))
(test 'small (size 3))
(test 'zero (size 0))
(test 'negative (size -2))

;; A guard sees the bindings the pattern made, and a false guard falls through
;; to the next clause rather than failing.
(test 'second (match '(1 2) ((a b) (guard #f) 'first) ((a b) 'second)))
(test 'first (match '(1 2) ((a b) (guard #t) 'first) ((a b) 'second)))

(test-end)

;; --------------------------------------------------------------------- errors
(test-begin "Errors")

(test #t (guard (e (#t (let ((m (error-object-message e)))
                         (and (string? m) #t))))
           (match 1 (2 'two))))

(test 'no-clauses (guard (e (#t 'no-clauses)) (match 1)))
(test 'no-body (guard (e (#t 'no-body)) (match 1 (1))))
(test 'bad-quote (guard (e (#t 'bad-quote)) (match 1 ((quote 1 2) 'x))))
(test 'bad-guard (guard (e (#t 'bad-guard)) (match 1 (n (guard) 'x))))

(test-end)

(test-end)
