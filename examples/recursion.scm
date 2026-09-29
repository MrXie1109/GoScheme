#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; Proper tail calls: loops that do not grow the stack.
;;;
;;; Run with:  goscheme examples/recursion.scm
;;;
;;; R7RS requires a tail call in tail position to be a jump, not a call, and the
;;; interpreter keeps that promise, so the loops below can run for millions of
;;; turns in constant stack space.

(import (scheme base) (scheme write))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

;;; ---------------------------------------------------------------- named let
;; The idiomatic loop: each turn reuses the same stack frame.
(show "a named let, half a million turns:"
      (let loop ((i 0) (sum 0))
        (if (= i 500000) sum (loop (+ i 1) (+ sum 1)))))

;;; ------------------------------------------------------- mutual recursion
;; Two procedures calling each other in tail position are one loop as well.
(define (count-even? n) (if (= n 0) #t (count-odd? (- n 1))))
(define (count-odd? n) (if (= n 0) #f (count-even? (- n 1))))
(show "mutual tail recursion:" (count-even? 100000))

;;; ------------------------------------------------------------------- cond
;; The last expression of a cond clause, and the bodies of when and unless, are
;; tail contexts too.
(show "a tail call in cond:"
      (let loop ((i 0)) (cond ((= i 100000) 'done) (else (loop (+ i 1))))))

;;; -------------------------------------------------- accumulator, the long way
(define (count-down n acc)
  (if (= n 0) acc (count-down (- n 1) (+ acc 1))))
(show "an explicit accumulator:" (count-down 100000 0))

;;; --------------------------------------------------------- and what is not
;; A call that is *not* in tail position does use stack, and Go's stack grows on
;; demand up to a limit, so keep this kind of recursion modest -- or write it as
;; a loop, as above.
(define (sum-to n) (if (= n 0) 0 (+ n (sum-to (- n 1)))))
(show "non-tail recursion to 10000:" (sum-to 10000))

(newline)
(display "recursion: end of tour") (newline)
