;;; SPDX-License-Identifier: MIT
;;;
;;; What the Guile benchmarks need, and nothing more.
;;;
;;; These are the same twelve workloads as bench/c, bench/python and
;;; bench/scheme.  Guile is the opponent that matters most: it is a real Scheme
;;; with a compiler and a JIT, so this is the comparison against *Scheme written
;;; well*, not against another language.
;;;
;;; The timing repeats until the clock has moved and reports time per run, for
;;; the same reason the C and Python ones do: one execution is microseconds.
(define-module (common)
  #:export (time-it))
(import (ice-9 time))

(define min-seconds 0.25)

;;; Run thunk until min-seconds have passed; print the mean per run.
(define (time-it name thunk)
  (let loop ((reps 0) (total 0) (t0 (get-internal-real-time)))
    (let* ((total (+ total (thunk)))
           (reps (+ reps 1))
           (elapsed (/ (- (get-internal-real-time) t0)
                       (exact->inexact internal-time-units-per-second))))
      (if (< elapsed min-seconds)
          (loop reps total t0)
          (begin
            (format #t "~a~14t~a~32t~,4f ms/run~48t~a runs~%"
                    name total (/ (* elapsed 1000) reps) reps)
            total)))))
