#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; Go flavoured concurrency in GoScheme: channels, (go ...) and (select ...).
;;;
;;; Run with:  goscheme examples/concurrency.scm

(import (scheme base) (scheme write) (goscheme channel))

(define (displayln . args)
  (for-each display args)
  (newline))

;;; ---------------------------------------------------------------- channels
;; An unbuffered channel is a rendezvous: the sender blocks until somebody
;; receives.  That makes the following deterministic, because chan-recv! waits
;; for the thread to arrive.
(define greeting (make-channel))
(go (chan-send! greeting 'hello))
(displayln "received: " (chan-recv! greeting))

;; A buffered channel holds up to n values, so the sender does not block.
(define queue (make-channel 3))
(chan-send! queue 1)
(chan-send! queue 2)
(chan-send! queue 3)
(displayln "buffered: " (chan-recv! queue) (chan-recv! queue) (chan-recv! queue))

;; chan-recv! returns two values; a closed channel reports ok? = #f.
(chan-close! queue)
(displayln "after close, ok? = "
           (call-with-values (lambda () (chan-recv! queue)) (lambda (v ok?) ok?)))

;;; ------------------------------------------------------------- worker pool
;; Four threads consume a closed job queue, exactly like a Go worker pool.
(define jobs (make-channel 32))
(define results (make-channel 64))

(define (worker)
  (let loop ()
    (call-with-values (lambda () (chan-recv! jobs))
      (lambda (job ok?)
        (if ok?
            (begin (chan-send! results (* job job)) (loop))
            (chan-send! results 'done))))))

(let loop ((i 0))
  (if (< i 4) (begin (go (worker)) (loop (+ i 1)))))

(let loop ((i 1))
  (if (<= i 10) (begin (chan-send! jobs i) (loop (+ i 1)))))
(chan-close! jobs)

(let loop ((seen 0) (sum 0) (workers 0))
  (if (< seen 14)
      (let ((v (chan-recv! results)))
        (if (eq? v 'done)
            (loop (+ seen 1) sum (+ workers 1))
            (loop (+ seen 1) (+ sum v) workers)))
      (begin
        (displayln "sum of squares 1..10 = " sum
                   " from " workers " workers"))))

;;; ------------------------------------------------------------------ select
;; select races several operations and runs the handler of whichever becomes
;; ready, like Go's select statement.  Note that (else) never waits, so it is
;; only reached when nothing else is ready.
(define fast (make-channel 1))
;; slow is unbuffered and has no receiver yet, so its send clause is *not*
;; ready: only one clause can win and the example is deterministic.  (When two
;; clauses are ready, Go picks one at random.)
(define slow (make-channel))
(chan-send! fast 'ready)

(select
  (chan-recv! fast) => (lambda (v) (displayln "got: " v))
  (chan-send! slow 42) => (lambda () (displayln "sent"))
  (after 200) => (lambda () (displayln "timed out"))
  (else) => (lambda () (displayln "nothing ready")))

;; With a timeout instead of (else), select waits for a sender.
(define late (make-channel))
(go (chan-send! late 'arrived))
(select
  (chan-recv! late) => (lambda (v) (displayln "waited for: " v))
  (after 200) => (lambda () (displayln "timed out")))

;;; ------------------------------------------------------- state in a thread
;; Share memory by communicating: the counter lives inside its own thread.
(define (make-counter)
  (let ((inbox (make-channel)))
    (go (let loop ((n 0))
          (call-with-values (lambda () (chan-recv! inbox))
            (lambda (msg ok?)
              (if ok?
                  (begin (chan-send! inbox (+ n 1)) (loop (+ n 1)))
                  #f)))))
    (lambda () (chan-send! inbox 'tick) (chan-recv! inbox))))

(define next! (make-counter))
(displayln "counter: " (next!) " " (next!) " " (next!))
