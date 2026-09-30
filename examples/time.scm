#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; Time and sleeping -- (goscheme time).
;;;
;;; Run with:  goscheme examples/time.scm
;;;
;;; Durations are plain numbers of milliseconds, the same unit the (after ms)
;;; clause of select takes.

(import (scheme base) (scheme write) (goscheme time))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

;;; ------------------------------------------------------------------ sleeping
(define before (monotonic-millisecond))
(sleep 20)
(define elapsed (- (monotonic-millisecond) before))
(show "a 20ms sleep took at least 20ms:" (>= elapsed 20) elapsed)
(show "sleeping for no time is allowed:" (begin (sleep 0) 'ok))

;;; ------------------------------------------------------------------ clocks
(show "milliseconds since 1970:" (> (current-millisecond) 1700000000000))
(show "the monotonic clock is only for differences:"
      (exact? (- (monotonic-millisecond) (monotonic-millisecond))))
;; R7RS makes current-second inexact and current-jiffy exact, and both are
;; still there next to the millisecond clocks.
(show "R7RS's own clocks: current-second is inexact, current-jiffy is exact:"
      (inexact? (current-second)) (exact? (current-jiffy)))

;;; ------------------------------------------------------- formatting, parsing
(define stamp 1759220122000)                     ; a fixed instant, in UTC
(show "formatted:" (time-format stamp "2006-01-02 15:04:05"))
(show "parsed back:" (time-parse "2026-09-30 09:15:22" "2006-01-02 15:04:05"))
(show "parse reports #f when the text does not fit:"
      (time-parse "not a date" "2006-01-02"))

;;; ---------------------------------------------------------------- the parts
;; An alist of exact integers, so a program can compute with them.
(define parts (time-utc-parts stamp))
(show "parts of that instant:" parts)
(show "the year, the month and the day:"
      (cdr (assq 'year parts)) (cdr (assq 'month parts)) (cdr (assq 'day parts)))
(show "the weekday is a number, 0 being Sunday:" (cdr (assq 'weekday parts)))
(show "one day is 86400000ms later:"
      (time-format (+ stamp 86400000) "2006-01-02"))

(newline)
(display "time: end of tour")
(newline)
