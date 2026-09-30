#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; Pattern matching -- (goscheme match).
;;;
;;; Run with:  goscheme examples/match.scm
;;;
;;; Patterns are matched against the shape of a value: _, a symbol (which
;;; binds), a literal, (), (p ... . rest), #(p ...), and the combinators
;;; (and ...), (or ...), (not ...).  A clause is (pattern body ...) or
;;; (pattern (guard test) body ...) -- the guard follows the pattern.

(import (scheme base) (scheme write) (goscheme match))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

;;; ------------------------------------------------- an evaluator for arithmetic
;; The pattern does the dispatching and the binding, so there is no cascade of
;; predicates to read.  Note that there are no predicate patterns — a test that
;; is not about shape belongs in a guard, as the number? clause below shows.
(define (evaluate expression)
  (match expression
    (n (guard (number? n)) n)
    (('add a b) (+ (evaluate a) (evaluate b)))
    (('sub a b) (- (evaluate a) (evaluate b)))
    (('mul a b) (* (evaluate a) (evaluate b)))
    (('neg a) (- (evaluate a)))
    (('quote datum) datum)
    (else (error "unknown expression" expression))))

(show "1 + 2 * 3:" (evaluate '(add 1 (mul 2 3))))
(show "negating a difference:" (evaluate '(neg (sub 10 4))))
(show "quoted data comes back untouched:" (evaluate '(quote (a b c))))
(show "an unknown form is an error:"
      (guard (e (#t 'unknown-expression)) (evaluate '(pow 2 3))))

;;; ------------------------------------------------------- shapes and literals
(define (describe value)
  (match value
    (() 'the-empty-list)
    (() 'unreachable)
    ((a b c) (list 'three-things a b c))
    ((a . rest) (list 'at-least-one a rest))
    (#(x y) (list 'two-element-vector x y))
    ("text" 'the-string-text)
    (0 'zero)
    ((and (or 1 2 3) n) (list 'small-number n))
    (n (guard (not (number? n))) 'not-a-number)
    (_ 'something-else)))

(for-each (lambda (v) (show "describe" v "->" (describe v)))
          (list '() '(1 2 3) '(1 2 3 4) #(7 8) "text" 0 2 42 'a-symbol))

;;; -------------------------------------------------------------- destructuring
(define (greet person)
  (match person
    (('person name age) (string-append name " is " (number->string age)))
    (('person name) (string-append name " is ageless"))
    (_ "not a person")))

(show (greet '(person "Ada" 36)))
(show (greet '(person "Grace")))

(newline)
(display "match: end of tour")
(newline)
