#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; The numeric tower, and the number syntax the reader accepts.
;;;
;;; Run with:  goscheme examples/numbers.scm
;;;
;;; Integers are exact and grow past 64 bits, rationals stay exact, and complex
;;; numbers are built in.  Nothing here is unusual for R7RS; it is here because
;;; it is the part of the report an implementation most often leaves out.

(import (scheme base) (scheme write))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

;;; -------------------------------------------------------------- big integers
(show "2^100:" (expt 2 100))
(show "30! :"
      (let loop ((i 1) (acc 1))
        (if (> i 30) acc (loop (+ i 1) (* acc i)))))
(show "arithmetic stays exact past the machine word:"
      (= (* 1000000000000 1000000000000) 1000000000000000000000000))

;;; ------------------------------------------------------------------ rationals
(show "1/3 stays a rational:" (/ 1 3))
(show "and the arithmetic closes:"
      (+ 1/3 1/6) (* 3 (/ 1 3)) (= 6/3 2) (exact-integer? 6/3))
(show "the parts:" (numerator 6/4) (denominator 6/4))
(show "rounding is exact on rationals:" (floor 7/2) (round 7/2))
(show "a decimal can be made exact:" (exact 2.5) #e1.5)

;;; ------------------------------------------------------------------- complex
(show "the square root of -1:" (sqrt -1))
(show "a rectangular number:" (make-rectangular 3 4))
(show "its magnitude:" (magnitude (make-rectangular 3 4)))
(show "real and imaginary parts:" (real-part 3+4i) (imag-part 3+4i))

;;; ------------------------------------------------------- exact and inexact
(show "mixing exact and inexact gives inexact:" (+ 1/3 0.5) (max 1 2.0))
(show "the irrational functions are inexact:"
      (sqrt 2) (exp 1) (expt 2.0 0.5))
(show "inexact numbers print with the shortest round trip:"
      (list 0.1 1e21 1.5e-9 (/ 1.0 3.0)))

;;; -------------------------------------------------------------- number syntax
(show "radix prefixes:" (list #x1f #b1011 #o17))
(show "exactness prefixes:" (list #e1.5 #i1/2))
(show "the exponent markers s f d l all mean the same:" (list 1s3 1f3 1d3 1l3))
(show "output in another radix:" (number->string 255 16) (number->string 255 2))
(show "and back again:" (string->number "ff" 16) (string->number "1/3"))

(newline)
(display "numbers: end of tour") (newline)
