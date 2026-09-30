#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; Locks, wait groups, one-time runs and atomics -- (goscheme sync).
;;;
;;; Run with:  goscheme examples/sync.scm
;;;
;;; Channels are how this dialect prefers to share state, but a lock is
;;; sometimes the honest tool, and Go's sync package is right underneath.

(import (scheme base) (scheme write) (goscheme sync))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

;;; ------------------------------------------------------- a lock, 100 threads
(define counter 0)
(define lock (make-mutex))
(define workers (make-waitgroup))

(let loop ((i 0))
  (if (< i 100)
      (begin
        (waitgroup-add! workers)
        (go (with-mutex lock (set! counter (+ counter 1)))
            (waitgroup-done! workers))
        (loop (+ i 1)))))

(waitgroup-wait workers)
(show "100 threads, a counter under a lock:" counter)
(show "the wait group is back to zero:" (waitgroup-count workers))
(show "and the lock is a value:" lock)

;; A lock is released however the body leaves, so a raise cannot leave it held.
(show "a raise inside with-mutex:"
      (guard (e (#t 'raised))
        (with-mutex lock (raise 'boom))))
(show "the lock still works afterwards:" (with-mutex lock 'usable))

;;; --------------------------------------------------- the same, without a lock
;; An atomic needs no lock, and every increment still lands.
(define hits (make-atomic))
(define bumpers (make-waitgroup))
(let loop ((i 0))
  (if (< i 100)
      (begin
        (waitgroup-add! bumpers)
        (go (atomic-add! hits 1) (waitgroup-done! bumpers))
        (loop (+ i 1)))))

(waitgroup-wait bumpers)
(show "100 threads, an atomic counter:" (atomic-ref hits))
(show "compare-and-set! reports whether it changed anything:"
      (atomic-compare-and-set! hits 100 0)
      (atomic-compare-and-set! hits 99 0))

;;; -------------------------------------------------------------- once
(define once (make-once))
(define (expensive)
  (display "  [the expensive part runs]")
  (newline)
  42)

(show "once, asked twice:" (once-run! once expensive) (once-run! once expensive))
(show "and it knows it has run:" (once-done? once))

(newline)
(display "sync: end of tour")
(newline)
