# (goscheme sync)

`(goscheme sync)` is the lock-and-counter half of GoScheme's Go-flavoured concurrency: mutexes, wait groups, one-shot initialisation and lock-free integer atomics for the interpreter threads started by `(go ...)`.  It wraps Go's `sync` and `sync/atomic` packages, and its with-mutex special form is a `syntax-rules` macro over `dynamic-wind`, so the lock a Scheme thread takes is a real Go mutex that may be taken in one thread and released in another.  Import it alongside the base library:

```scheme
(import (scheme base) (goscheme sync))
```

## Procedures

### Mutexes

| Procedure | Arguments | Description |
|---|---|---|
| `make-mutex` | `(make-mutex)` | Returns a new unlocked mutex.  A mutex has no owning thread: any interpreter thread may lock or unlock it, which makes it a raw building block rather than an automatically scoped region. |
| `mutex?` | `(mutex? obj)` | Returns `#t` if obj is a mutex and `#f` otherwise.  Never raises. |
| `mutex-lock!` | `(mutex-lock! mutex)` | Acquires mutex, blocking the calling interpreter thread until it is free, and returns an unspecified value.  The underlying lock is not reentrant: locking a mutex the same thread already holds deadlocks. |
| `mutex-unlock!` | `(mutex-unlock! mutex)` | Releases mutex and returns an unspecified value.  Unlocking a mutex that is not currently locked is a Go runtime fatal error — `fatal error: sync: unlock of unlocked mutex` — which aborts the whole process and cannot be caught or recovered.  Every lock must be matched by exactly one unlock. |
| `with-mutex` | `(with-mutex mutex body ...)` | Special form.  Locks mutex, evaluates body ..., and releases the lock however the body leaves: a normal return, a raised condition, or a continuation escape.  Returns the value of the last body expression. |

### Wait groups

| Procedure | Arguments | Description |
|---|---|---|
| `make-waitgroup` | `(make-waitgroup)` | Returns a new wait group whose counter is 0. |
| `waitgroup?` | `(waitgroup? obj)` | Returns `#t` if obj is a wait group and `#f` otherwise.  Never raises. |
| `waitgroup-add!` | `(waitgroup-add! wg [n])` | Adds n to the counter and returns an unspecified value; n defaults to 1 and must be an exact integer that fits in a signed 64-bit word.  It may be negative as long as the counter does not become negative; a decrement that would take it below zero raises `waitgroup-add!: count would go negative`. |
| `waitgroup-done!` | `(waitgroup-done! wg)` | Decrements the counter by one and returns an unspecified value.  Raises `waitgroup-done!: count is already zero` when the counter is already zero, where Go's WaitGroup would panic. |
| `waitgroup-count` | `(waitgroup-count wg)` | Returns the current counter as an exact integer without blocking. |
| `waitgroup-wait` | `(waitgroup-wait wg)` | Blocks the calling interpreter thread until the counter reaches zero, then returns an unspecified value.  Add everything you are going to add before waiting; as in Go, adding concurrently with a wait is a misuse. |

### One-shot initialisation

| Procedure | Arguments | Description |
|---|---|---|
| `make-once` | `(make-once)` | Returns a new once object that has not run yet. |
| `once?` | `(once? obj)` | Returns `#t` if obj is a once object and `#f` otherwise.  Never raises. |
| `once-run!` | `(once-run! once thunk)` | Runs thunk, a procedure of no arguments, the first time and returns the value it produced; every later call returns that same remembered value without running thunk again.  The first call holds the once object's lock for the whole run, so a second thread blocks until the value is ready.  If the thunk raises, the run does not count: the condition propagates, the once object stays not-done, and a later call tries again. |
| `once-done?` | `(once-done? once)` | Returns `#t` once a run has completed successfully, and `#f` before it or after a run that raised. |

### Atomics

| Procedure | Arguments | Description |
|---|---|---|
| `make-atomic` | `(make-atomic [initial])` | Returns a new atomic integer, initialised to initial when it is supplied and 0 otherwise.  initial must be an exact integer that fits in a signed 64-bit word. |
| `atomic?` | `(atomic? obj)` | Returns `#t` if obj is an atomic and `#f` otherwise.  Never raises. |
| `atomic-ref` | `(atomic-ref atomic)` | Returns the current value as an exact integer.  The read is atomic and never blocks. |
| `atomic-set!` | `(atomic-set! atomic n)` | Stores n and returns an unspecified value. |
| `atomic-add!` | `(atomic-add! atomic n)` | Adds n and returns the new value; the whole read-add-write is one atomic operation. |
| `atomic-swap!` | `(atomic-swap! atomic n)` | Stores n and returns the previous value. |
| `atomic-compare-and-set!` | `(atomic-compare-and-set! atomic old new)` | Stores new only if the current value equals old, returning `#t` when it did and `#f` when it did not.  The comparison and the store are one atomic operation. |

## Notes

* **Sharing.** Mutexes, wait groups, once objects and atomics are Go values, so they are safe to share between interpreter threads; atomics are lock-free.  Ordinary Scheme data — pairs, strings, vectors, records — is not synchronised, so share memory by communicating.  The interpreter's own global environment, ports and parameters are synchronised.
* **Mutex ownership and failure.** A mutex has no owner, so any thread may unlock it.  It is not reentrant (locking twice in one thread deadlocks), and unlocking one that is not locked is a Go fatal error that aborts the process and cannot be caught, so always pair each lock with exactly one unlock.
* **with-mutex.** The macro releases the lock on any exit, including a raised condition or a continuation escape, and re-locks if the escaped dynamic extent is re-entered.  Because a continuation cannot be resumed from another thread, one captured inside with-mutex must not be resumed while another thread waits for the lock.
* **Counters.** waitgroup-add! accepts a negative n as long as the counter never goes below zero, and waitgroup-done! raises instead of panicking at zero.  Add before calling waitgroup-wait: adding to a wait group while another thread is waiting is a misuse in Go and can panic the runtime.
* **Blocking.** mutex-lock!, waitgroup-wait and the first once-run! on a given once object block only the calling interpreter thread; the others keep running.  mutex-unlock!, waitgroup-count and every atomic operation never block.
* **One-shot execution.** The first once-run! evaluates thunk on a fresh child interpreter machine with its own continuation stack (it does not start another goroutine), so the caller's pending dynamic-wind and exception frames do not surround it; its value is remembered once it succeeds, and a failed run is not.  Calling once-run! for the same once object from inside its own thunk deadlocks.
* **Types.** Passing the wrong kind of object raises an ordinary catchable condition naming the expected type, such as `mutex-lock!: expected a mutex but got 5`.  Atomics require exact integers that fit in a signed 64-bit word: a bignum raises `integer does not fit in 64 bits`, and an inexact value such as 1.5 raises too.
* **Platform.** These are goroutines and the standard library's `sync` and `sync/atomic` packages, present on every platform GoScheme builds for.  Scheduling is not deterministic, so only synchronisation is guaranteed, not the relative order in which threads run.

## Example

```scheme
(import (scheme base) (scheme write) (goscheme sync) (goscheme channel))

;; Eight workers, each bumping a mutex-guarded counter and a lock-free
;; atomic, then checking in with the wait group.
(define lock (make-mutex))
(define counter 0)
(define wg (make-waitgroup))
(define total (make-atomic 0))
(define workers 8)

(waitgroup-add! wg workers)
(let loop ((i 0))
  (when (< i workers)
    (go
      (with-mutex lock (set! counter (+ counter 1)))
      (atomic-add! total 1)
      (waitgroup-done! wg))
    (loop (+ i 1))))

(waitgroup-wait wg)
(display (list counter (atomic-ref total) (waitgroup-count wg)))
(newline)
;; prints (8 8 0)
```
