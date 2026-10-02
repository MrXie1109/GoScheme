# The GoScheme guide

One file that walks through the interpreter and its libraries.  The
[main README](../README.md) is the reference, [`docs/extensions/`](../extensions/README.md)
and [`docs/srfi/`](../srfi/README.md) are the procedure-by-procedure lookup
tables, and `examples/` has more programs to read.

## 1. Running it

```sh
make build                        # produces .build/goscheme
./.build/goscheme program.scm     # run a file
./.build/goscheme -e '(display (+ 1 2))'   # run an expression
./.build/goscheme                 # the REPL
```

A script is compiled to bytecode and run on a stack machine where the compiler
understands it, and interpreted where it does not; `-interp` forces the
tree-walker, which is how the two are compared.  `goscheme compile` writes the
compiled program to a `.scmc` file, and a `.scmc` file runs without being parsed
as source:

```sh
./.build/goscheme compile tool.scm -o tool.scmc
./.build/goscheme tool.scmc
./.build/goscheme -interp tool.scm     # the tree-walker, for comparison
```

[docs/bytecode.md](../bytecode.md) has the details: what compiles, what is left
to the interpreter, the file format, and the measured numbers.

`(command-line)` is the **script name followed by the user's arguments** — the
interpreter's own name never appears, which is the deliberate departure from
R7RS 6.14, so `(cdr (command-line))` is exactly the argument list.

```scheme
;; args.scm
(display (command-line)) (newline)
;; $ ./.build/goscheme args.scm one two
;; => ("args.scm" "one" "two")
```

## 2. The language in one page

```scheme
(define (square x) (* x x))              ; a procedure
(define add (lambda (a b) (+ a b)))      ; the same thing spelled out
(let ((x 2) (y 3)) (+ x y))              ; => 5
(let* ((x 2) (y (* x x))) y)             ; => 4      (each binding sees the last)
(letrec ((even? (lambda (n) (or (= n 0) (odd? (- n 1)))))
         (odd?  (lambda (n) (and (> n 0) (even? (- n 1))))))
  (even? 10))                            ; => #t
(cond ((assv 2 '((1 . a) (2 . b))) => cdr) (else 'none))   ; => b
(case 3 ((1 2) 'low) ((3 4) 'high) (else 'other))          ; => high
(do ((i 0 (+ i 1)) (acc '() (cons i acc))) ((= i 3) acc))  ; => (2 1 0)

;; (continue) is Go's: it abandons the rest of a do body and goes on with the
;; step expressions, which still run.  A procedure the body calls may use it
;; too, because it is dynamic rather than lexical.
(define kept '())
(do ((i 0 (+ i 1))) ((= i 5) (reverse kept))
  (if (odd? i) (continue))
  (set! kept (cons i kept)))                 ; => (0 2 4)
```

Recursion in tail position does not grow the stack, and `call/cc` is
multi-shot: the continuation can be called again after it returns.

```scheme
(define (loop n acc) (if (= n 0) acc (loop (- n 1) (+ acc 1))))
(loop 1000000 0)                          ; => 1000000, no stack growth

(call-with-values (lambda () (values 1 2)) (lambda (a b) (+ a b)))  ; => 3
(guard (e ((symbol? e) (list 'caught e))) (raise 'boom))            ; => (caught boom)
```

`define-record-type`, `define-syntax` with `syntax-rules`, `parameterize`,
`dynamic-wind` and `delay`/`force` are all R7RS-small; the
[README](../README.md#language-coverage) has the full list.

```scheme
(define-record-type point (make-point x y) point? (x point-x) (y point-y))
(point-x (make-point 3 4))                ; => 3

(define-syntax swap!
  (syntax-rules ()
    ((_ a b) (let ((tmp a)) (set! a b) (set! b tmp)))))
```

A library is imported by name; one that is not built in is loaded from the
search path, so a program can carry its own `lib/greet.sld`.

```scheme
(import (scheme base) (scheme write) (goscheme fast) (srfi 1))
```

## 3. Numbers

Integers are exact and of arbitrary size, rationals are exact, and a result
becomes inexact only when an inexact value takes part.

```scheme
(+ 1/3 1/6)            ; => 1/2
(expt 2 100)           ; => 1267650600228229401496703205376
(/ 1 3)                ; => 1/3
(exact->inexact 1/3)   ; => 0.3333333333333333
(exact 2.0)            ; => 2
(floor/ 7 2)           ; => 3 and 1
(number->string 255 16) ; => "ff"
```

`(goscheme fast)` adds the integer work that is slow in Scheme: `expt-mod`,
`isqrt`, `prime?`, `primes`, `factor`, and the bit operations
`bit-and`/`-or`/`-xor`/`-not`/`-shift`/`-count`.

```scheme
(expt-mod 2 1000 1000000007)   ; => 688423210
(factor 1234567890)            ; => (2 3 3 5 3607 3803)
(length (primes 100000))       ; => 9592
(integer-length 255)           ; => 8
```

## 4. Data and text

Lists have R7RS and SRFI-1; vectors have R7RS, SRFI-133 and the batch lane of
`(goscheme fast)`.  Watch the fold argument order: SRFI-1's `fold` calls
`(kons element accumulator)`, while `(goscheme fast)`'s `fold-left` calls
`(proc accumulator element)`.

```scheme
(fold - 0 '(1 2 3))                       ; => 2   SRFI-1
(fold-left - 0 '(1 2 3))                  ; => -6  (goscheme fast)
(fold-right cons '() '(1 2 3))            ; => (1 2 3)
(take-while (lambda (x) (< x 3)) '(1 2 3 4))   ; => (1 2)
(lset-union = '(1 2) '(2 3))              ; => (1 2 3)
```

Vectors: the batch procedures take one Go pass over the whole vector, and the
`!` variants write in place.

```scheme
(vector-add #(1 2 3) #(10 20 30))         ; => #(11 22 33)
(vector-prefix-sum #(1 2 3))              ; => #(1 3 6)
(let ((v (vector-iota 4))) (vector-scale! v 2) v)   ; => #(0 2 4 6)
(call-with-values (lambda () (vector-partition even? #(1 2 3 4)))
  (lambda (v k) (list v k)))              ; => (#(2 4 1 3) 2)
```

Strings are indexed by character, not byte: `string-length` counts characters
and `string-byte-length` counts UTF-8 bytes.

```scheme
(string-fields "  a  b ")                 ; => ("a" "b")
(string-lines "a\r\nb\r\n")               ; => ("a" "b")
(string-titlecase "hello WORLD")          ; => "Hello World"
(string-split "a:b:c" ":" 2)              ; => ("a" "b:c")
(string-find-all "aaaa" "aa")             ; => (0 2)
```

Hash tables, `match` for destructuring, JSON and regular expressions:

```scheme
(import (goscheme hash-table) (goscheme match) (goscheme json) (goscheme regexp))

(define t (make-hash-table))              ; an equal? table
(hash-table-set! t "a" 1)
(hash-table-ref/default t "a" 'missing)   ; => 1

(match '(1 2 3)
  ((a b c) (+ a b c))
  ((_ ... rest) 'longer))                 ; => 6

(json-parse "{\"n\": 42}")                ; => an equal? table
(regexp-match-positions (regexp "a+") "baa")   ; => ((1 . 3)) — byte offsets
```

## 5. Concurrency

Threads are Go goroutines, channels are Go channels, and `(go ...)` is how you
start one.  The interpreter's own state, channels, mutexes and atomics are
safe to share; ordinary Scheme data (pairs, strings, vectors, records) is not,
so communicate through channels instead.

```scheme
(import (goscheme channel) (goscheme sync))

(define ch (make-channel))
(go (chan-send! ch (* 2 21)))
(chan-recv! ch)                           ; => 42

(select
  ((chan-recv! ch) => (lambda (v) v))
  ((after 100) => (lambda (ms) 'timeout))
  (else => (lambda () 'nothing-ready)))
```

A worker pool with a wait group, and the mutex form that releases however the
body leaves:

```scheme
(define jobs (make-channel 4))
(define results (make-channel 4))
(define wg (make-waitgroup))
(waitgroup-add! wg 1)
(go (with-mutex some-mutex (chan-send! results 'done)) (waitgroup-done! wg))
(waitgroup-wait wg)
```

A *closed* channel is always ready, so a clause that reads one keeps winning; a
*nil* channel is never ready, so assigning one is how a clause is retired:

```scheme
;; (set! ch (nil-channel)) is Go's ch = nil: the clause can never be chosen
;; again, while close would make it win immediately and for ever.
(set! ch (nil-channel))
(select (chan-recv! ch) => (lambda (v) 'got)
        (else) => (lambda () 'idle))     ; => idle
```

`go-wait` waits for every thread started so far, so leave long-lived servers
until last.  Two misuses abort rather than raise: unlocking a mutex you do not
hold, and an `(after ms)` clause with a negative or non-integer `ms`.

## 6. I/O and the operating system

Ports are R7RS: `read-line`, `read`, `write`, `display`,
`with-output-to-file`, `call-with-input-file`, `open-input-string`.  A socket
connection is an ordinary port in both directions.

```scheme
(import (goscheme fs) (goscheme process) (goscheme socket) (goscheme http) (goscheme time))

(glob "*.scm")                            ; a list of paths
(path-extension "notes/a.txt")            ; => ".txt"
(system* "echo" "hello")                  ; => 0 (the exit status)
(define in (open-input-process "printf" "%s\n" "piped"))
(read-line in)                            ; => "piped"
(close-port in)
(process-status in)                       ; => 0 once closed

(sleep 10)                                ; milliseconds
(monotonic-millisecond)                   ; a monotonic clock reading
```

A tiny server, and the client that talks to it:

```scheme
(define listener (tcp-listen 0))
(go (let ((c (tcp-accept listener)))
      (write-string (string-append (read-line c) "\n") c)
      (close-port c)))
(define c (tcp-connect "127.0.0.1" (tcp-listener-port listener)))
(write-string "ping\n" c)
(read-line c)                             ; => "ping"
```

## 7. Performance

The interpreter is a tree-walker over an explicit continuation stack, so it is
not the fastest Scheme; the README's
[Performance section](../README.md#performance) has the measurements.
`(goscheme fast)` is where the slow jobs went, and the rule that decides
whether it helps is simple:

* a **builtin** predicate or comparison (`<`, `string<?`, `even?`, `string?`)
  runs inside the Go loop, so `filter`, `sort`, `count`, `any`, `every`,
  `delete-duplicates` and the folds over `+`/`*`/`max`/`min` are fast;
* a predicate **you wrote** is called once per element, which costs about what
  the Scheme loop costs, so there the library is convenience, not speed.

```scheme
(define v (vector-iota 100000))
(filter even? v)                          ; Go loop
(filter (lambda (x) (even? x)) v)         ; one call per element
```

Measure with a clock that does not move, more than once: `current-millisecond`
is wall-clock, `monotonic-millisecond` is the right one for a duration.

```scheme
(import (goscheme time))
(define (timed thunk)
  (let ((start (monotonic-millisecond)))
    (let ((v (thunk))) (list v (- (monotonic-millisecond) start)))))
(car (timed (lambda () (length (sort (iota 20000))))))
```

## 8. Embedding and building

`goscheme build` turns a script into an executable with the interpreter and the
source inside it; `-o` names the output (default `a.out`, or `a.exe` on
Windows) and `-static` resolves the libraries up front so the program cannot
fail on a missing file at run time.

```sh
./.build/goscheme build script.scm -o mytool
./.build/goscheme build -static server.scm -o server
./dist/goscheme-linux-amd64 program.scm
```

The interpreter is also a Go package; the README's
[Embedding section](../README.md#embedding-in-a-go-program) and
`examples/embed/` have the full surface, which is small:

```go
import goscheme "github.com/MrXie1109/GoScheme"

i := goscheme.New()
i.Define("double", 1, 1, func(args []goscheme.Value) (goscheme.Value, error) {
    n, _ := args[0].Int()
    return goscheme.Int(n * 2), nil
})
v, _ := i.Eval("(double 21)")             // 42
```

## 9. Pitfalls

* **A later `import` shadows an earlier one.** Importing two libraries that
  export the same name with different bindings leaves whichever came last.
  That is why `(goscheme fast)`, `(srfi 1)` and `(srfi 133)` share one binding
  for every name they have in common.
* **`eqv?` and `equal?` are not `=`.** `(equal? 1 1.0)` is `#f` while `(= 1
  1.0)` is `#t`.
* **Ordinary data is not synchronized.** Use channels, or `(goscheme sync)`.
* **Two misuses abort instead of raising:** `mutex-unlock!` on a mutex you do
  not hold is a Go runtime fatal error, and `(after ms)` with a bad `ms`
  panics in a way `guard` cannot catch.
* **`select` picks at random** among clauses that are ready at the same time;
  the written order decides only the order of evaluation.
* **`json-write` writes hash tables, not alists**, and sorts the keys.
* **Regexp positions are byte offsets**; the match procedures also accept a
  bare pattern string.
* **Multiple values in a single-value context are truncated** to the first.
* **The process does not wait for goroutines at exit**; call `go-wait`.
* `display` on cyclic data may not terminate, and `write-simple` may not
  either, which R7RS allows.

## 10. Where to go next

* [`docs/extensions/`](../extensions/README.md) — every `(goscheme ...)`
  procedure, its arguments and its errors.
* [`docs/srfi/`](../srfi/README.md) — SRFI-1, 2, 8, 26, 111, 128 and 133.
* `examples/` — runnable programs for each library, listed in
  [`examples/README.md`](../../examples/README.md).
* [README](../README.md) — the reference: language coverage, implementation
  notes, testing, cross-compilation.
