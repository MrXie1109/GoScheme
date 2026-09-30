;;; SPDX-License-Identifier: MIT
;;; The small SRFI libraries: (srfi 2) and-let*, (srfi 8) receive,
;;; (srfi 26) cut and cute, and (srfi 111) boxes.

(import (scheme base) (scheme write) (chibi test)
        (srfi 2) (srfi 8) (srfi 26) (srfi 111))

(test-begin "Small SRFIs")

;; ---------------------------------------------------------------- (srfi 2)
(test-begin "and-let*")

(test 1 (and-let* () 1))
(test #t (and-let* ()))
(test 1 (and-let* ((x 1))))
(test 2 (and-let* ((x 1) (y 2))))
(test 20 (and-let* ((x 2) ((even? x))) (* x 10)))
(test #f (and-let* ((x 1) ((even? x))) (* x 10)))
(test #f (and-let* ((x 1) ((even? x)))))
(test #f (and-let* ((x #f)) 1))
(test '(2) (and-let* ((x '(1 2)) (y (cdr x))) y))
(test 'big (and-let* ((x 5) ((> x 3))) 'big))
(test 5 (let ((x 5)) (and-let* (x) x)))
(test #f (let ((x 5)) (and-let* (x (> x 9)) 'big)))
;; A claw that fails stops the ones after it.
(test 1 (let ((n 0))
          (and-let* ((x (begin (set! n (+ n 1)) 1))) n)))
(test #f (and-let* ((x #f) (y 2)) 'body))
(test 0 (let ((n 0))
          (and-let* ((x #f) (y (begin (set! n 1) 1))) 'body)
          n))
;; The bindings are visible to later claws.
(test 3 (and-let* ((x 1) (y (+ x 1)) (z (+ y 1))) z))
(test-end)

;; ---------------------------------------------------------------- (srfi 8)
(test-begin "receive")

(test '(1 2) (receive (a b) (values 1 2) (list a b)))
(test '(3 1) (receive (q r) (floor/ 7 2) (list q r)))
(test '(1 2 3) (receive all (values 1 2 3) all))
(test '(1 (2 3)) (receive (a . rest) (values 1 2 3) (list a rest)))
(test 'ok (receive () (values) 'ok))
(test 6 (receive (a b) (values 2 3) (* a b)))
(test-end)

;; --------------------------------------------------------------- (srfi 26)
(test-begin "cut and cute")

(test 3 ((cut + 1 <>) 2))
(test '(1 2) ((cut list <> <>) 1 2))
(test '(1 2 3) ((cut list <...>) 1 2 3))
(test '(1 . 2) ((cut cons <> 2) 1))
(test '(1 2) ((cut list 1 2)))
(test '(a 3 b 4) ((cut list <> 3 <> 4) 'a 'b))
(test '(2 4 6) (map (cut * 2 <>) '(1 2 3)))
(test 6 ((cut apply + <...>) '(1 2 3)))
(test '(1 2 3) ((cut list 1 <...>) 2 3))
(test '(1 2) ((cute list 1 <>) 2))
(test '(1 3) (let ((n 1))
               (define f (cute list n <>))
               (set! n 2)
               (f 3)))
;; cut, unlike cute, evaluates the ordinary arguments at call time.
(test '(2 3) (let ((n 1))
               (define f (cut list n <>))
               (set! n 2)
               (f 3)))
(test 10 (let ((n 5)) (define f (cute * n <>)) (set! n 100) (f 2)))
(test-end)

;; -------------------------------------------------------------- (srfi 111)
(test-begin "Boxes")

(test #t (box? (box 1)))
(test #f (box? 1))
(test #f (box? '(1)))
(test 'v (unbox (box 'v)))
(test 42 (let ((b (box 1))) (set-box! b 42) (unbox b)))
(test 1 (unbox (box 1)))
(test #t (let ((b (box 1))) (eq? b b)))
(test #f (equal? (box 1) (box 1)))
(test 3 (let ((b (box 1))) (set-box! b (+ (unbox b) 2)) (unbox b)))
(test 'not-a-box (guard (e (#t 'not-a-box)) (unbox 5)))
(test 'not-a-box (guard (e (#t 'not-a-box)) (set-box! 5 1)))
;; A box holds whatever it is given, including another box.
(test 1 (unbox (unbox (box (box 1)))))
(test-end)

(test-end)
