;;; SPDX-License-Identifier: MIT
;;; Concurrency extension: channels, (go ...), (select ...) and go-wait.

(import (scheme base) (scheme write) (scheme char) (chibi test))

(test-begin "Concurrency")

;; ---------------------------------------------------------------- channels
(test-begin "Channels")

(test #t (channel? (make-channel)))
(test #f (channel? 42))
(test #t (chan-open? (make-channel)))

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
(test #f (chan-open? closed))
;; channel-open? was an alias of chan-open? and is gone; a program that used it
;; gets an ordinary unbound-variable error.
(test 'gone (guard (e (#t 'gone)) (eval '(channel-open? 1) (interaction-environment))))
;; Closing twice is a no-op.
(test 'ok (begin (chan-close! closed) 'ok))
;; A closed channel is always ready for select.
(test 'closed (select (chan-recv! closed) => (lambda (v) 'closed)))

;; A nil channel is the opposite: never ready, which is how a clause is
;; disabled — (set! ch (nil-channel)) is Go's ch = nil.
(test #t (channel? (nil-channel)))
(test #t (nil-channel? (nil-channel)))
(test #f (nil-channel? (make-channel)))
(test #f (nil-channel? 5))
;; It is not closed, so chan-open? still says #t; what it is not is ready.
(test #t (chan-open? (nil-channel)))
(test 'error (guard (e (#t 'error)) (chan-close! (nil-channel))))
(test 'timeout (select (chan-recv! (nil-channel)) => (lambda (v) 'got)
                       (after 20) => (lambda () 'timeout)))
(test 'timeout (select (chan-send! (nil-channel) 1) => (lambda () 'sent)
                       (after 20) => (lambda () 'timeout)))
(test 'idle (select (chan-recv! (nil-channel)) => (lambda (v) 'got)
                    (else) => (lambda () 'idle)))

;; The whole point: a closed clause keeps winning, and assigning a nil channel
;; to it takes it out of the race.
(define retirable (make-channel 1))
(chan-send! retirable 'x)
(chan-close! retirable)
(test 'closed (select (chan-recv! retirable) => (lambda (v) 'closed)
                      (else) => (lambda () 'idle)))
(set! retirable (nil-channel))
(test 'idle (select (chan-recv! retirable) => (lambda (v) 'got)
                    (else) => (lambda () 'idle)))

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

;; ------------------------------------------------------- sync: mutex and friends
(test-begin "Sync")

(test #t (mutex? (make-mutex)))
(test #f (mutex? 5))

;; with-mutex runs its body under the lock and returns its value.
(test 3 (with-mutex (make-mutex) (+ 1 2)))

;; The lock is released however the body leaves, so a raise does not leave it
;; held forever.
(define mu (make-mutex))
(test 'raised (guard (e (#t 'raised)) (with-mutex mu (raise 'boom))))
(test 'usable (with-mutex mu 'usable))

;; The explicit pair works too.
(mutex-lock! mu)
(test 'locked (begin (mutex-unlock! mu) 'locked))

;; A lock actually serialises threads: every increment is seen.
(define shared 0)
(define guards (make-mutex))
(define threads (make-waitgroup))
(let loop ((i 0))
  (if (< i 200)
      (begin
        (waitgroup-add! threads)
        (go (with-mutex guards (set! shared (+ shared 1)))
            (waitgroup-done! threads))
        (loop (+ i 1)))))
(waitgroup-wait threads)
(test 200 shared)
(test 0 (waitgroup-count threads))
(test #t (waitgroup? threads))
(test #f (waitgroup? mu))

;; add!/done! keep the count honest instead of panicking in Go.
(test 'negative (guard (e (#t 'negative)) (waitgroup-add! (make-waitgroup) -1)))
(test 'underflow (guard (e (#t 'underflow)) (waitgroup-done! (make-waitgroup))))

;; once-run! runs the thunk the first time and remembers the value.
(define once-ran 0)
(define o (make-once))
(test #t (once? o))
(test #f (once? 5))
(test 7 (once-run! o (lambda () (set! once-ran (+ once-ran 1)) 7)))
(test 7 (once-run! o (lambda () (set! once-ran (+ once-ran 1)) 99)))
(test 1 once-ran)
(test #t (once-done? o))

;; A thunk that raises does not count as the first run.
(define o2 (make-once))
(test 'boom (guard (e (#t 'boom)) (once-run! o2 (lambda () (raise 'boom)))))
(test #f (once-done? o2))
(test 5 (once-run! o2 (lambda () 5)))

;; Atomics need no lock, and every increment lands.
(define counter (make-atomic))
(test #t (atomic? counter))
(test 0 (atomic-ref counter))
(atomic-set! counter 10)
(test 10 (atomic-ref counter))
(test 15 (atomic-add! counter 5))
(test 15 (atomic-swap! counter 3))
(test 3 (atomic-ref counter))
(test #t (atomic-compare-and-set! counter 3 4))
(test #f (atomic-compare-and-set! counter 99 5))
(test 4 (atomic-ref counter))

(define hits (make-atomic))
(define bumpers (make-waitgroup))
(let loop ((i 0))
  (if (< i 200)
      (begin
        (waitgroup-add! bumpers)
        (go (atomic-add! hits 1) (waitgroup-done! bumpers))
        (loop (+ i 1)))))
(waitgroup-wait bumpers)
(test 200 (atomic-ref hits))

(test-end)

(test-end)
