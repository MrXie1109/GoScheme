# (goscheme channel)

`(goscheme channel)` exposes Go's concurrency model to Scheme: channels, interpreter threads and a select form that races several operations at once.  It is built directly on Go channels and goroutines, so the blocking rules are Go's rules, and each `(go ...)` body runs on a real goroutine with its own continuation stack while sharing the global environment, libraries, ports and parameters.  Import it alongside the base library:

```scheme
(import (scheme base) (goscheme channel))
```

## Procedures

### Channels

| Procedure | Arguments | Description |
|---|---|---|
| `make-channel` | `(make-channel [capacity])` | Returns a new channel.  With no argument, or a capacity of 0, it is unbuffered — a rendezvous where every send waits for a receiver.  A positive capacity makes it buffered, able to hold up to that many values.  capacity must be an exact integer from 0 to 2^40; anything else raises an error. |
| `channel?` | `(channel? obj)` | Returns `#t` if obj is a channel and `#f` otherwise.  Never raises. |
| `chan-open?` | `(chan-open? ch)` | Returns `#t` while ch is open and `#f` once it has been closed.  Raises an error if ch is not a channel. |
| `chan-send!` | `(chan-send! ch v)` | Sends v and returns an unspecified value.  Blocks until a receiver takes the value (unbuffered) or buffer space frees up (buffered).  Any Scheme value may be sent.  Raises a condition if ch is already closed, or if another thread closes it while the send is blocked. |
| `chan-recv!` | `(chan-recv! ch)` | Receives and returns two values: the value and `#t`.  Blocks until a value is available.  On a closed channel it returns at once — values still in the buffer are delivered with `#t`, and once the buffer is drained it returns the unspecified value and `#f`.  Raises an error if ch is not a channel. |
| `chan-close!` | `(chan-close! ch)` | Closes ch and returns an unspecified value.  Closing an already-closed channel is a no-op.  Raises an error if ch is not a channel, and a nil channel cannot be closed at all. |
| `nil-channel` | `(nil-channel)` | Returns the Scheme spelling of Go's nil channel: a channel that is never ready.  Sending to it and receiving from it block for ever, and `select` never picks a clause that uses it, which is how a program disables one of its own clauses — `(set! ch (nil-channel))` is Go's `ch = nil`. |
| `nil-channel?` | `(nil-channel? object)` | `#t` for a nil channel, `#f` for any other channel and for anything else. |

### Interpreter threads

| Procedure | Arguments | Description |
|---|---|---|
| `go` | `(go body ...)` | Special form.  Starts body ... on a new interpreter thread (a goroutine) and returns an unspecified value immediately.  The thread shares the global environment, libraries, ports and standard parameters but has its own continuation stack.  At least one body expression is required, otherwise an error is raised.  An uncaught error is printed on the current error port and ends only that thread; `(exit)` inside a thread likewise ends only that thread. |
| `go-wait` | `(go-wait)` | Blocks until every thread started by `(go ...)` so far has finished, then returns an unspecified value.  There is no per-thread handle, so a long-lived server loop must not still be running when it is called. |

### Selection

| Procedure | Arguments | Description |
|---|---|---|
| `select` | `(select [clause ...])` | Special form.  Races clauses written as `(operation) => handler` triples and returns the value of the handler whose operation is ready.  The operations are `(chan-recv! ch)`, whose handler is called with the received value; `(chan-send! ch v)`, whose handler is called with no arguments and which raises if ch is closed; `(after ms)`, whose handler is called with no arguments once ms milliseconds pass; and `(else)`, or a bare else, whose handler is called with no arguments only when no other clause is ready.  Every channel expression, send value, timer and handler is evaluated once before the race begins.  If several operations are ready at the same instant, one is chosen pseudo-randomly, as in Go; a receive from a closed and drained channel is ready and yields the unspecified value.  With no clauses at all the form blocks forever. |

## Notes

* **Blocking.** A send waits for a receiver or buffer space, a receive waits for a value, go-wait waits for every thread started so far, and select waits until some clause can proceed.  Only the calling interpreter thread blocks; the other threads keep running.
* **Closed is not nil, and the difference matters in `select`.** A *closed*
  channel is always ready: a receive takes whatever is buffered and then the
  zero value, and a send raises.  A *nil* channel is never ready: both block
  for ever.  So a loop that discharges several channels and wants to drop one
  from the race must assign `(nil-channel)` to it, not close it — closing it
  makes its clause win immediately and for ever.
* **Uncaught conditions in a thread.** A thread that raises without a handler
  prints `go: uncaught error: ...` on the error port and ends; the other
  threads keep running.
* **Buffered versus unbuffered.** An unbuffered channel is a rendezvous, so a thread cannot send to itself.  A buffered channel lets a sender run ahead, and a thread may even send to and then receive from its own channel, so hand-off protocols want an unbuffered channel.
* **Closed channels.** Closing twice is a no-op.  A receive from a closed channel never blocks: buffered values are still delivered, and once they are gone the receive yields the unspecified value and `#f`.  Sending on a closed channel, or losing a select race to a concurrent close, raises a condition rather than panicking.
* **select details.** At most one `(after ms)` clause and at most one `(else)` clause are allowed, and a bare else is accepted.  ms must be a non-negative exact integer; an invalid timer raises an error that aborts the current form rather than a condition a guard can catch.  An else clause never waits, so it turns select into a non-blocking poll.  Clause order fixes only the order in which the operands are evaluated, not which ready clause wins.
* **Threads and sharing.** Threads share the global environment, libraries, ports and the standard parameters, all of which are internally synchronised, and channels are safe to share between them.  Ordinary Scheme data — pairs, strings, vectors, records — is not synchronised, so share memory by communicating.  Continuations belong to the thread that captured them; resuming one from another thread raises an error.
* **Errors inside a thread.** An uncaught error is printed on the current error port as `go: uncaught error: ...` and ends only that thread; `(exit)` inside a thread also ends just that thread.  The process does not wait for threads when the program ends, so call `(go-wait)` (or receive their results) if they must finish.
* **Deadlock.** A script in which every thread blocks is reported by the Go runtime as `all goroutines are asleep - deadlock!` and aborts, exactly as a Go program would.  The REPL is protected instead: a form that blocks forever simply waits, and Ctrl-C abandons it and returns to the prompt.

## Example

```scheme
(import (scheme base) (scheme write) (goscheme channel))

;; A three-stage pipeline: producer -> squarer -> collector.
(define numbers (make-channel))
(define squares (make-channel))
(define results (make-channel))

(go
  (let loop ((i 1))
    (if (<= i 5)
        (begin (chan-send! numbers i) (loop (+ i 1)))
        (chan-close! numbers))))

(go
  (let loop ()
    (call-with-values (lambda () (chan-recv! numbers))
      (lambda (v ok)
        (if ok
            (begin (chan-send! squares (* v v)) (loop))
            (chan-close! squares))))))

(go
  (let loop ((acc '()))
    (call-with-values (lambda () (chan-recv! squares))
      (lambda (v ok)
        (if ok
            (loop (cons v acc))
            (chan-send! results (reverse acc)))))))

(display (chan-recv! results))
(newline)
(go-wait)
;; prints (1 4 9 16 25)
```
