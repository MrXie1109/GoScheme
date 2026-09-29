;;; SPDX-License-Identifier: MIT
;;; Concurrency extension: channels, (go ...), (select ...) and go-wait.

(import (scheme base) (scheme write) (scheme char) (chibi test))

(test-begin "Concurrency")

;; ---------------------------------------------------------------- channels
(test-begin "Channels")

(test #t (channel? (make-channel)))
(test #f (channel? 42))
(test #t (channel-open? (make-channel)))

;; A buffered channel accepts up to its capacity without blocking.
(define buffered (make-channel 3))
(chan-send! buffered 1)
(chan-send! buffered 2)
(chan-send! buffered 3)
(test 1 (chan-recv! buffered))
(test 2 (chan-recv! buffered))
(test 3 (chan-recv! buffered))

;; An unbuffered channel is a rendezvous: the sender blocks until a receiver
;; arrives, so this is deterministic.
(define rendezvous (make-channel))
(go (chan-send! rendezvous 'hello))
(test 'hello (chan-recv! rendezvous))

;; chan-recv! delivers the value and a flag.
(test '(hello #t)
      (call-with-values
       (lambda ()
         (let ((c (make-channel)))
           (go (chan-send! c 'hello))
           (chan-recv! c)))
       list))

;; The send really blocks: the second send cannot happen until the first
;; value has been taken.
(define c (make-channel))
(define order (make-channel))
(go (chan-send! c 'first) (chan-send! order 'sent))
(chan-recv! c)
(test 'sent (chan-recv! order))

;; Closing: receives yield ok? = #f, sends are an error.
(define closed (make-channel 1))
(chan-send! closed 'last)
(chan-close! closed)
(test 'last (chan-recv! closed))
(test #f (call-with-values (lambda () (chan-recv! closed)) (lambda (v ok) ok)))
(test #t (call-with-values (lambda () (chan-recv! closed)) (lambda (v ok) (eq? v (if #f #f)))))
(test 'error (guard (e (#t 'error)) (chan-send! closed 1)))
(test #f (channel-open? closed))
;; Closing twice is a no-op.
(test 'ok (begin (chan-close! closed) 'ok))
;; A closed channel is always ready for select.
(test 'closed (select (chan-recv! closed) => (lambda (v) 'closed)))

(test-end)

;; --------------------------------------------------------------- go / join
(test-begin "Threads")

;; go-wait waits for every thread started so far.
(define acc (make-channel 128))
(let loop ((i 0))
  (if (< i 100)
      (begin (go (chan-send! acc i)) (loop (+ i 1)))))
(go-wait)
(let loop ((i 0) (sum 0))
  (if (< i 100)
      (loop (+ i 1) (+ sum (chan-recv! acc)))
      (test 4950 sum)))

;; A thread may start further threads; go-wait waits transitively.  The
;; channel is buffered because go-wait runs *before* the receive: an
;; unbuffered send would block forever, exactly as it would in Go.
(define nested (make-channel 1))
(go (go (chan-send! nested 'nested)))
(go-wait)
(test 'nested (chan-recv! nested))

;; Worker pool: four threads consume a closed job queue and report.
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
  (if (<= i 20) (begin (chan-send! jobs i) (loop (+ i 1)))))
(chan-close! jobs)
(let loop ((seen 0) (sum 0) (done 0))
  (if (< seen 24)
      (let ((v (chan-recv! results)))
        (if (eq? v 'done)
            (loop (+ seen 1) sum (+ done 1))
            (loop (+ seen 1) (+ sum v) done)))
      (begin
        (test 2870 sum)          ;; 1^2 + ... + 20^2
        (test 4 done))))

;; Threads share the global environment and the ports.
(define shared-port (open-output-string))
(go (write-string "from-thread" shared-port))
(go-wait)
(test "from-thread" (get-output-string shared-port))

(test-end)

;; ----------------------------------------------------------- error handling
(test-begin "Thread errors")

;; A guarded error inside a thread is handled there.
(define err (make-channel))
(go (guard (e (#t (chan-send! err (error-object-message e)))) (error "boom")))
(test "boom" (chan-recv! err))

;; An *uncaught* error inside a thread is reported on the error port and does
;; not stop the rest of the program.
(go (car 5))
(go-wait)
(test 'alive (begin (go (chan-send! err 'alive)) (chan-recv! err)))

;; A continuation belongs to the thread that captured it: resuming it from
;; another thread is an error, reported to that thread's handler.
(define cross (make-channel 1))
(let ((k #f))
  (call/cc (lambda (c) (set! k c) 'captured))
  (go (guard (e (#t (chan-send! cross 'refused))) (k 'jumped))))
(test 'refused (chan-recv! cross))

(test-end)

;; ------------------------------------------------------------------- select
(test-begin "Select")

;; The ready receive wins.
(define a (make-channel 1))
(define b (make-channel 1))
(chan-send! a 'from-a)
(test 'from-a (select (chan-recv! a) => (lambda (v) v)
                      (chan-recv! b) => (lambda (v) 'from-b)))

;; When both are ready either may be chosen, as in Go.
(chan-send! b 'from-b)
(test #t (let ((r (select (chan-recv! a) => (lambda (v) v)
                          (chan-recv! b) => (lambda (v) v))))
           (or (eq? r 'from-a) (eq? r 'from-b))))

;; A send case.
(define out (make-channel 1))
(test 'sent (select (chan-send! out 42) => (lambda () 'sent)))
(test 42 (chan-recv! out))

;; select blocks until a sender appears (no else / after clause).
(define late (make-channel))
(go (chan-send! late 'late))
(test 'late (select (chan-recv! late) => (lambda (v) v)))

;; (after ms) fires when nothing is ready.
(test 'timeout (select (chan-recv! (make-channel)) => (lambda (v) 'got)
                       (after 30) => (lambda () 'timeout)))

;; (else) never waits.
(test 'idle (select (chan-recv! (make-channel)) => (lambda (v) 'got)
                    (else) => (lambda () 'idle)))

;; (else) takes precedence over a timeout that has not expired.
(test 'idle (select (chan-recv! (make-channel)) => (lambda (v) 'got)
                    (after 5000) => (lambda () 'timeout)
                    (else) => (lambda () 'idle)))

;; A ready channel beats both (after) and (else).
(define ready (make-channel 1))
(chan-send! ready 'ready)
(test 'ready (select (chan-recv! ready) => (lambda (v) v)
                     (after 5000) => (lambda () 'timeout)
                     (else) => (lambda () 'idle)))

;; The handler receives the value for a receive clause and nothing for the
;; others, and the value of select is the value of the handler.
(test 21 (select (chan-recv! (make-channel)) => (lambda (v) 'no)
                 (after 5) => (lambda () 21)))

;; A hand-off protocol needs an *unbuffered* channel: with a buffer the
;; thread can consume its own message and run ahead.
(define ping (make-channel))
(define count 0)
(go (let loop ((i 0))
      (if (< i 20000)
          (begin (chan-send! ping i) (chan-recv! ping) (loop (+ i 1)))
          (chan-close! ping))))
(test 'done (let loop ()
              (call-with-values (lambda () (chan-recv! ping))
                (lambda (v ok?)
                  (if ok?
                      (begin (set! count (+ count 1)) (chan-send! ping v) (loop))
                      'done)))))
(test 20000 count)

(test-end)

;; ------------------------------------------------------- long lived threads
;; This section runs last on purpose: the counter thread below never
;; terminates, and go-wait waits for every thread started so far.
(test-begin "Stateful threads")

;; A counter owned by its own thread: state is shared by communicating.
(define (make-counter)
  (let ((ch (make-channel)))
    (go (let loop ((n 0))
          (call-with-values (lambda () (chan-recv! ch))
            (lambda (msg ok?)
              (if ok?
                  (begin (chan-send! ch (+ n 1)) (loop (+ n 1)))
                  #f)))))
    (lambda () (chan-send! ch 'tick) (chan-recv! ch))))
(define tick! (make-counter))
(test 1 (tick!))
(test 2 (tick!))
(test 3 (tick!))

(test-end)

(test-end)
