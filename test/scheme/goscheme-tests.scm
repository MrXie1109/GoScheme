;;; SPDX-License-Identifier: MIT
;;; Supplementary tests for GoScheme: proper tail calls, continuations,
;;; dynamic-wind, libraries, records, ports and the extension surface.

(import (scheme base) (scheme write) (scheme char) (scheme lazy)
        (scheme inexact) (scheme complex) (scheme file) (scheme read)
        (scheme eval) (scheme process-context) (scheme case-lambda)
        (scheme time) (scheme load) (chibi test))

(test-begin "GoScheme")

;; ------------------------------------------------------------------ tail calls
(test-begin "Proper tail calls")

(test 1000000 (let loop ((i 0)) (if (= i 1000000) i (loop (+ i 1)))))

(define (even2? n) (if (= n 0) #t (odd2? (- n 1))))
(define (odd2? n) (if (= n 0) #f (even2? (- n 1))))
(test #t (even2? 100000))
(test #t (apply even2? (list 100000)))
(test 'cond-done (let loop ((i 0)) (cond ((= i 200000) 'cond-done) (else (loop (+ i 1))))))
(test 'when-done (let loop ((i 0)) (if (= i 200000) 'when-done (begin (loop (+ i 1))))))
(test #t (let loop ((i 0)) (or (= i 300000) (loop (+ i 1)))))
(test #f (let loop ((i 0)) (and (< i 300000) (loop (+ i 1)))))
(test 300000 (let loop ((i 0)) (if (= i 300000) i (loop (+ i 1)))))
(test 'case-done (let loop ((i 0)) (case i ((200000) 'case-done) (else (loop (+ i 1))))))
(test 100000 (let loop ((i 0)) (if (= i 100000) i (loop (call-with-values (lambda () (+ i 1)) (lambda (x) x))))))
(test 50000 (let loop ((i 0)) (if (= i 50000) i (loop (force (delay (+ i 1)))))))

(test-end)

;; ------------------------------------------------------------- continuations
(test-begin "Continuations")

(test 42 (call/cc (lambda (k) (+ 1 (k 42)))))
(test '(1 2 3) (call/cc (lambda (k) (k '(1 2 3)))))
(test 5 (+ 1 (call/cc (lambda (k) 4))))
(test 'no-return (call/cc (lambda (k) (k 'no-return) (error "unreachable"))))

;; Re-entrant (multi-shot) continuations.
(test 3
      (let ((k #f) (n 0))
        (let ((v (call/cc (lambda (c) (set! k c) 0))))
          (set! n (+ n 1))
          (if (< n 3) (k n) n))))

;; A generator built with call/cc.
(define (make-generator lst)
  (define saved-k #f)
  (define return #f)
  (define (yield v)
    (call/cc (lambda (k) (set! saved-k k) (return v))))
  (define (start)
    (for-each yield lst)
    (return 'done))
  (lambda ()
    (call/cc (lambda (r)
               (set! return r)
               (if saved-k (saved-k #f) (start))))))

(define gen (make-generator '(a b c)))
(test 'a (gen))
(test 'b (gen))
(test 'c (gen))
(test 'done (gen))

;; call/cc inside map / for-each with an escape.
(test 3 (call/cc (lambda (k) (for-each (lambda (x) (if (= x 3) (k x))) '(1 2 3 4)) #f)))

(test-end)

;; ------------------------------------------------------------ dynamic-wind
(test-begin "dynamic-wind")

(define trace '())
(define (note x) (set! trace (cons x trace)))

(test 'escaped
      (call/cc (lambda (k)
                 (dynamic-wind
                  (lambda () (note 'before))
                  (lambda () (note 'body) (k 'escaped) (note 'unreachable))
                  (lambda () (note 'after))))))
(test '(after body before) trace)

;; dynamic-wind runs the before thunks again when a continuation re-enters.
(define trace2 '())
(define k2 #f)
(define n2 0)
(call/cc (lambda (k) (set! k2 k)))
(dynamic-wind
 (lambda () (set! trace2 (cons 'in trace2)))
 (lambda () (set! n2 (+ n2 1)))
 (lambda () (set! trace2 (cons 'out trace2))))
(if (< n2 3) (k2 #f))
(test 3 n2)
(test '(out in out in out in) trace2)

;; dynamic-wind passes through the value of its thunk.
(test 42 (dynamic-wind (lambda () #f) (lambda () 42) (lambda () #f)))

;; Escaping nested dynamic-winds through a continuation runs every after
;; thunk, innermost first.
(define dw-log '())
(test 'escaped
      (call/cc (lambda (k)
                 (dynamic-wind
                  (lambda () (set! dw-log (cons 'in1 dw-log)))
                  (lambda ()
                    (dynamic-wind
                     (lambda () (set! dw-log (cons 'in2 dw-log)))
                     (lambda () (k 'escaped))
                     (lambda () (set! dw-log (cons 'out2 dw-log)))))
                  (lambda () (set! dw-log (cons 'out1 dw-log)))))))
(test '(out1 out2 in2 in1) dw-log)

;; guard must unwind dynamic-wind too.
(define g-log '())
(test 'caught
      (guard (e (#t 'caught))
        (dynamic-wind
         (lambda () (set! g-log (cons 'in g-log)))
         (lambda () (raise 'x))
         (lambda () (set! g-log (cons 'out g-log))))))
(test '(out in) g-log)

(test-end)

;; ------------------------------------------------------------- multiple values
(test-begin "Multiple values")
(test '(1 2 3) (call-with-values (lambda () (values 1 2 3)) list))
(test 6 (call-with-values (lambda () (values 1 2 3)) +))
(test 0 (call-with-values (lambda () (values)) (lambda () 0)))
(test 6 (let-values (((a b) (values 1 2)) ((c) (values 3))) (+ a b c)))
(test 6 (let*-values (((a b) (values 1 2)) ((c) (values (+ a b)))) (+ a b c)))
(test 3 (let-values () 3))
(test '(1 2 3) (call-with-values (lambda () (apply values '(1 2 3))) list))
(test 'multi (call/cc (lambda (k) (call-with-values (lambda () (k 'multi)) (lambda (x) x)))))
(test-end)

;; -------------------------------------------------------------------- macros
(test-begin "Macros")

;; Hygiene: a macro-introduced temporary cannot capture a user binding.
(define-syntax my-or
  (syntax-rules ()
    ((_) #f)
    ((_ e) e)
    ((_ e1 e2 ...)
     (let ((temp e1)) (if temp temp (my-or e2 ...))))))
(test 8 (let ((temp 8)) (my-or #f #f temp)))
(test 'ok (let ((if list)) (my-or #f #f 'ok)))

;; Recursive macros with ellipsis.
(define-syntax my-list
  (syntax-rules ()
    ((_) '())
    ((_ x y ...) (cons x (my-list y ...)))))
(test '(1 2 3 4) (my-list 1 2 3 4))

;; Nested ellipsis.
(define-syntax my-let*
  (syntax-rules ()
    ((_ () body ...) (let () body ...))
    ((_ ((n v) rest ...) body ...) (let ((n v)) (my-let* (rest ...) body ...)))))
(test 6 (my-let* ((a 1) (b 2) (c 3)) (+ a b c)))

(define-syntax my-cond
  (syntax-rules (else)
    ((_ (else e ...)) (begin e ...))
    ((_ (test e ...) clause ...) (if test (begin e ...) (my-cond clause ...)))))
(test 'b (my-cond (#f 'a) (#t 'b)))

;; Tail patterns after the ellipsis.
(define-syntax last-two
  (syntax-rules ()
    ((_ a ... x y) (list x y))))
(test '(3 4) (last-two 1 2 3 4))

;; define-syntax inside a body.
(test 9 (let ()
          (define-syntax sq (syntax-rules () ((_ x) (* x x))))
          (sq 3)))

(test-end)

;; ----------------------------------------------------------------- libraries
(test-begin "Libraries")

(define-library (goscheme demo)
  (export double triple (rename quadruple quad) demo-constant)
  (import (scheme base))
  (begin
    (define demo-constant 7)
    (define (double x) (* 2 x))
    (define (triple x) (* 3 x))
    (define (quadruple x) (* 4 x))))

(import (goscheme demo))
(test 10 (double 5))
(test 15 (triple 5))
(test 20 (quad 5))
(test 7 demo-constant)

(import (prefix (goscheme demo) d:))
(test 10 (d:double 5))
(import (only (goscheme demo) triple))
(test 15 (triple 5))
(import (except (goscheme demo) double))
(test 12 (quad 3))
(test #t (and (member 'r7rs (features)) #t))

(define-library (goscheme counter)
  (export make-counter)
  (import (scheme base))
  (begin
    (define (make-counter)
      (let ((n 0))
        (lambda () (set! n (+ n 1)) n)))))
(import (goscheme counter))
(define c1 (make-counter))
(define c2 (make-counter))
(test 1 (c1))
(test 2 (c1))
(test 1 (c2))

(test-end)

;; ------------------------------------------------------------------ records
(test-begin "Records")

(define-record-type <point>
  (make-point x y)
  point?
  (x point-x set-point-x!)
  (y point-y))

(define p (make-point 3 4))
(test #t (point? p))
(test #f (point? '(3 . 4)))
(test 3 (point-x p))
(test 4 (point-y p))
(set-point-x! p 10)
(test 10 (point-x p))
(test #f (equal? (make-point 1 2) (make-point 1 2)))
(test 2 (point-y (make-point (point-x p) 2)))

(test-end)

;; --------------------------------------------------------------------- ports
(test-begin "Ports")

(test "hello" (let ((o (open-output-string)))
                (write-string "hello" o)
                (get-output-string o)))
(test "(1 2 3)" (let ((o (open-output-string)))
                 (write '(1 2 3) o)
                 (get-output-string o)))
(test "(1 2 3)" (let ((o (open-output-string)))
                  (display '(1 2 3) o)
                  (get-output-string o)))
(test "#0=(1 . #0#)"
      (let ((x (list 1)) (o (open-output-string)))
        (set-cdr! x x)
        (write x o)
        (get-output-string o)))
(test "abc" (let ((i (open-input-string "abc def")))
               (read-string 3 i)))
(test 'def (let ((i (open-input-string "abc def")))
             (read-string 3 i)
             (read i)))
(test '(1 2 3) (read (open-input-string "(1 2 3)")))
(test #u8(1 2 3) (let ((o (open-output-bytevector)))
                   (write-bytevector #u8(1 2 3) o)
                   (get-output-bytevector o)))
(test 2 (let ((i (open-input-bytevector #u8(1 2 3))))
          (read-u8 i)
          (read-u8 i)))
(test "line" (let ((i (open-input-string "line\nnext")))
               (read-line i)))

;; File round trip.
(define tmp-file (string-append "goscheme-tmp-" (number->string (current-jiffy)) ".txt"))
(call-with-output-file tmp-file
  (lambda (out) (write '(hello "world" 42) out)))
(test '(hello "world" 42) (call-with-input-file tmp-file read))
(test #t (file-exists? tmp-file))
(delete-file tmp-file)
(test #f (file-exists? tmp-file))

(test-end)

;; ----------------------------------------------------------------- exceptions
(test-begin "Exceptions")

(test 'caught
      (call/cc (lambda (k)
                 (with-exception-handler
                  (lambda (e) (k 'caught))
                  (lambda () (raise 'boom))))))
(test 43 (with-exception-handler (lambda (e) 42) (lambda () (+ 1 (raise-continuable 'x)))))
(test 'inner (guard (e ((symbol? e) 'inner)) (raise 'oops)))
(test 42 (guard (e (#t 42)) (error "bad" 1 2)))
(test "bad" (guard (e (#t (error-object-message e))) (error "bad" 1 2)))
(test '(1 2) (guard (e (#t (error-object-irritants e))) (error "bad" 1 2)))
(test #t (guard (e ((error-object? e) #t)) (error "bad")))
(test #f (guard (e ((file-error? e) #t) (else #f)) (error "bad")))
(test 'reraised (guard (outer (#t 'reraised))
                  (guard (inner ((string? inner) 'nope))
                    (raise 'other))))
(test 3 (guard (e (#t 'error)) 3))

(test-end)

;; ------------------------------------------------------------------ eval/load
(test-begin "Eval")

(test 3 (eval '(+ 1 2) (environment '(scheme base))))
(test 8 (eval '(let ((x 3)) (+ x 5)) (interaction-environment)))
(test 1024 (eval '(expt 2 10) (environment '(scheme base))))
(test 'ok (eval '(begin (define eval-test-var 1) 'ok) (interaction-environment)))

(test-end)

;; --------------------------------------------------- include / cond-expand
(test-begin "Include and cond-expand")

(include "include-me.scm")
(test 99 included-value)
(test 42 (included-double 21))

;; include is relative to the including file's directory.
(define-library (goscheme included)
  (export included-lib-value)
  (import (scheme base))
  (include-library-declarations "include-lib.sld"))
(import (goscheme included))
(test 'from-included-library included-lib-value)

(test 'yes (cond-expand (r7rs 'yes) (else 'no)))
;; The interpreter is right on either platform; the expectation has to follow
;; it, which is what running this suite on Windows showed.
(test (if (memq 'windows (features)) 'no 'yes)
      (cond-expand ((and r7rs (not windows)) 'yes) (else 'no)))
(test 'yes (cond-expand ((library (scheme base)) 'yes) (else 'no)))
(test 'no (cond-expand ((library (no such library)) 'yes) (else 'no)))
(test 'yes (cond-expand ((or nonexistent-feature r7rs) 'yes) (else 'no)))
(test 'fallback (cond-expand (nonexistent-feature 'yes) (else 'fallback)))

(test-end)

;; ------------------------------------------------------------- hash tables
(test-begin "Hash tables")

(define ht (make-equal-hashtable))
(test 0 (hash-table-size ht))
(hash-table-set! ht '(1 2) 'a)
(hash-table-set! ht "k" 2)
(test 2 (hash-table-size ht))
(test 'a (hash-table-ref ht '(1 2)))
(test 2 (hash-table-ref/default ht "k" #f))
(test 'missing (hash-table-ref/default ht 'nope 'missing))
(test #t (hash-table-exists? ht '(1 2)))
(hash-table-update! ht "k" (lambda (x) (+ x 1)))
(test 3 (hash-table-ref ht "k"))
(hash-table-delete! ht '(1 2))
(test #f (hash-table-exists? ht '(1 2)))
(test 1 (hash-table-size ht))

(define total 0)
(hash-table-walk ht (lambda (k v) (set! total (+ total v))))
(test 3 total)

(define eh (make-eq-hashtable))
(hash-table-set! eh 'x 1)
(test 1 (hash-table-ref/default eh 'x #f))
(test 'not-there (hash-table-ref/default eh (list 'x) 'not-there))

(test 2 (hash-table-size (alist->hash-table '((a . 1) (b . 2)))))

;; hash returns a non-negative exact integer, equal keys agree, and an optional
;; bound restricts the result (SRFI 125).
(test #t (exact? (hash 'x)))
(test #t (>= (hash 'x) 0))
(test #t (= (hash '(1 2)) (hash (list 1 2))))
(test 0 (hash 'x 1))
(test #t (let ((h (hash "anything" 16))) (and (<= 0 h) (< h 16))))
(test #t (guard (e (#t #t)) (hash 'x 0)))
(test #t (guard (e (#t #t)) (hash 'x -1)))

(test-end)

;;; ------------------------------------------- defects found by an outside review
;;; Every one of these was a real bug, with the shape it was reported in.
(test-begin "Reviewed defects")

;; A1: an argument error raised by a primitive written with def must be
;; catchable, like one raised by a primitive written with defSimple.
(test 'caught (guard (e (#t 'caught)) (apply 5 '())))
(test 'caught (guard (e (#t 'caught)) (map 5 '(1))))
(test 'caught (guard (e (#t 'caught)) (vector-map 5 (vector 1))))
(test 'caught (guard (e (#t 'caught)) (for-each 5 '(1 2))))
(test 'caught (guard (e (#t 'caught)) (string-map 5 "ab")))
(test 'caught (guard (e (#t 'caught)) (dynamic-wind 5 (lambda () 1) (lambda () 2))))

;; A2/A3: a decimal literal with #e is exact, and a malformed number is not a
;; number at all.
(test 1/10 #e0.1)
(test #t (= #e0.1 1/10))
(test (expt 10 23) #e1e23)
(test 3/2 #e1.5)
(test #f (string->number "1e"))
(test #f (string->number "1.5s"))
(test #f (string->number "--1"))
(test #f (string->number "#x--ff"))
(test #t (symbol? (string->symbol "1e")))

;; A5: parameterize applies the converter to the value it installs, not to the
;; value it puts back.
(define review-parameter (make-parameter 10 (lambda (x) (* x 2))))
(test '(20 6 20)
      (list (review-parameter)
            (parameterize ((review-parameter 3)) (review-parameter))
            (review-parameter)))

;; A6: internal definitions follow letrec*, so a name defined later in the body
;; is not an outer binding of the same name.
(define review-outer 'outer)
(test 'uninitialised
      (guard (e (#t 'uninitialised))
        ((lambda () (define a review-outer) (define review-outer 2) a))))
(test 'uninitialised
      (guard (e (#t 'uninitialised))
        (let () (define a review-outer) (define review-outer 2) a)))
(test 1 ((lambda () (define a 1) (define b a) b)))

;; A9/A10: exact and inexact numbers compare exactly, and a NaN is not zero.
(test #t (> 9007199254740993 9007199254740992.0))
(test #f (<= 9007199254740993 9007199254740992.0))
(test #t (> 1/3 0.3333333333333333))
(test #f (zero? +nan.0))
(test #f (positive? +nan.0))
(test #f (negative? +nan.0))

;; B1: a datum label on a vector makes it circular.
(test #t (let ((v (read (open-input-string "#1=#(1 #1#)"))))
           (eq? v (vector-ref v 1))))
(test #t (let ((v (read (open-input-string "#1=(1 . #1#)"))))
           (eq? v (cdr v))))

;; B3: a closed port is an error to read from, an output port has no character
;; to be ready, and the byte procedures want a binary port.
(test 'closed (guard (e (#t 'closed))
               (let ((p (open-input-string "a"))) (close-port p) (read-line p))))
(test 'closed (guard (e (#t 'closed)) (char-ready? (open-output-string))))
(test 'binary (guard (e (#t 'binary)) (read-u8 (open-input-string "abc"))))

;; B4: invalid UTF-8 is reported, not replaced with U+FFFD.
(test 'bad (guard (e (#t 'bad)) (utf8->string #u8(255 254))))
(test "hi" (utf8->string #u8(104 105)))

;; B5: a key that is not equivalent is not found, whatever its hash.
(test 'missing
      (let ((h (make-eqv-hashtable)))
        (hash-table-set! h +nan.0 'nan)
        (hash-table-ref/default h +nan.0 'missing)))
(test 'found (let ((h (make-equal-hashtable)))
               (hash-table-set! h '(1 2) 'found)
               (hash-table-ref/default h (list 1 2) 'missing)))

;; B6: the curried define means what it says.
(test '(1 2) (let () (define ((f a) b) (list a b)) ((f 1) 2)))

;; C: a value count that does not match, or a variable named twice, is an error
;; rather than a silent adjustment.
(test 'few (guard (e (#t 'few)) (let-values (((a b) (values 1))) a)))
(test 'many (guard (e (#t 'many)) (let-values (((a) (values 1 2))) a)))
(test 'duplicate (guard (e (#t 'duplicate)) (let ((x 1) (x 2)) x)))
(test 'duplicate (guard (e (#t 'duplicate)) (letrec ((x 1) (x 2)) x)))

(test-end)

;;; -------------------------------------------------------------- reader odds
(test-begin "Reader")

;; The unspecified value is printed as #!unspecified, so it has to read back,
;; on its own and as a list element.
(test #t (eq? #!unspecified (if #f #f)))
(test #t (eq? #!unspecified (read (open-input-string "#!unspecified"))))
(test 2 (length (list 1 #!unspecified)))
(test 1 (length (list #!unspecified)))
(test "#!unspecified"
      (let ((o (open-output-string))) (write (if #f #f) o) (get-output-string o)))

;; A shebang line is skipped, so a script can be executable directly.
(test 'ok (read (open-input-string "#!/usr/bin/env goscheme\nok")))

;; A bad #! word is reported as itself, even inside a list, instead of being
;; reported as an unterminated list.
(test "string:1: unknown directive #!bogus"
      (guard (e (#t (error-object-message e)))
        (read (open-input-string "(#!bogus)"))))

;; A genuinely unfinished list is still an unfinished list.
(test #t (guard (e (#t #t)) (read (open-input-string "(1 2"))))

(test-end)

;; ------------------------------------------------------------------ records of
;; behaviour that the reference suite does not stress.
(test-begin "Misc")

(test 7 (let-values (((q r) (floor/ 13 2))) (+ q r)))
(test 'many ((case-lambda ((x) 'one) ((x y) 'two) (args 'many)) 1 2 3))
(test 5 ((case-lambda ((x) x) ((x y) (+ x y))) 2 3))
(test "abc" (list->string (string->list "abc")))
(test '#(1 2 3) (list->vector '(1 2 3)))
(test '(1 2 3) (vector->list #(1 2 3)))
(test 3 (vector-length (vector-append #(1) #(2 3))))
(test "abc" (string-append "a" "b" "c"))
(test #\b (string-ref "abc" 1))
(test 3 (string-length "abc"))
(test '(#\c #\b #\a) (reverse (string->list "abc")))
(test 6 (apply + 1 2 '(3)))
(test '(3 4 5) (map + '(1 2 3) '(2 2 2)))
(test 6 (call-with-values (lambda () (values 1 2 3)) (lambda args (apply + args))))
(test 10 (let loop ((i 0) (acc 0)) (if (= i 5) acc (loop (+ i 1) (+ acc 2)))))
(test 3 (length (list 1 2 3)))
(test #t (eqv? 100000000000000000000 100000000000000000000))
(test #f (eqv? 1 1.0))
(test #t (equal? (vector 1 (list 2 3)) (vector 1 (list 2 3))))
(test 4 (string->number "4"))
(test #f (string->number "four"))
(test 'many (force (delay-force (delay-force (delay 'many)))))
(test 1 (let ((count 0))
          (define p (delay (begin (set! count (+ count 1)) count)))
          (force p)
          (force p)
          count))
(test-end)


;; ------------------------------------------------------------ subprocesses
(test-begin "Subprocesses")

(define (posix-system?) (and (memq 'posix (features)) #t))

(if (posix-system?)
    (begin
      ;; system runs a command line through the shell; system* execs directly.
      (test 0 (system "exit 0"))
      (test 3 (system "exit 3"))
      (test 0 (system* "true"))
      (test 1 (system* "false"))
      (test 7 (system* "sh" "-c" "exit 7"))
      ;; killed by a signal: reported the way a shell does
      (test 143 (system* "sh" "-c" "kill -TERM $$"))
      ;; a program that cannot be started at all is a file error
      (test #t (guard (e ((file-error? e) #t)) (system* "/nonexistent-program-xyz")))
      ;; the child inherits the interpreter's standard streams, so capture
      ;; its output through a file
      (define sys-file (string-append "goscheme-subprocess-"
                                      (number->string (current-jiffy)) ".txt"))
      (test 0 (system (string-append "printf 'one\\ntwo\\n' > " sys-file)))
      (test "one" (call-with-input-file sys-file read-line))
      (test 0 (system (string-append "echo three >> " sys-file)))
      (test '("one" "two" "three")
            (call-with-input-file sys-file
              (lambda (p)
                (let loop ((acc '()))
                  (let ((line (read-line p)))
                    (if (eof-object? line) (reverse acc) (loop (cons line acc))))))))
      (delete-file sys-file)
      (test #f (file-exists? sys-file)))
    (test #t #t))

(test-end)


;; -------------------------------------------------- libraries from files
;; A library that is not built in is looked for on the search path: (lib greet)
;; is lib/greet.sld relative to this file's directory.
(test-begin "Libraries from files")

(import (lib greet) (lib math) (lib use-include))
(test "hello world" (greet "world"))
(test "HELLO WORLD" (greet-loud "world"))
(test 49 (square-of-sum 3 4))              ;; (lib math) imports (lib greet)
(test 20 (twice (lambda (x) (* x 2)) 5))   ;; include inside a library

;; The import modifiers work on libraries loaded from files too.
(import (prefix (lib greet) g:))
(test "hello p" (g:greet "p"))
(import (only (lib math) square-of-sum))
(test 9 (square-of-sum 1 2))
(import (rename (lib greet) (greet hi)))
(test "hello r" (hi "r"))
(import (except (lib greet) greet-loud))

;; cond-expand can ask whether a library is available.
(test #t (cond-expand ((library (lib greet)) #t) (else #f)))
(test #f (cond-expand ((library (no such library)) #t) (else #f)))

;; A cycle between libraries is an error, not a hang.
(test 'caught (guard (e (#t 'caught)) (import (lib cycle-a))))
;; A file that defines a different library is an error too.
(test 'caught (guard (e (#t 'caught)) (import (lib mismatch))))

(test-end)


;; ----------------------------------------------------- foreign functions
;; Loading C functions needs a build made with cgo; the static binaries say so
;; instead of failing obscurely, so this section checks both behaviours.
(test-begin "Foreign functions")

(if (memq 'ffi (features))
    (begin
      (import (goscheme ffi))

      ;; Where the C library lives is the platform's business: libm on Linux,
      ;; libSystem on macOS, and the Universal CRT on Windows, which is where
      ;; the string and math functions live (the older msvcrt.dll does not have
      ;; cbrt, and GetProcAddress on the executable itself finds no imports).
      (define windows? (if (memq 'windows (features)) #t #f))
      (define math-lib-name
        (cond (windows? "ucrtbase.dll")
              ((memq 'darwin (features)) "libSystem.B.dylib")
              (else "libm.so.6")))
      (define math-lib (load-shared-library math-lib-name))
      (define c-lib (load-shared-library math-lib-name))
      (define (c-name posix windows) (if windows? windows posix))

      (define cbrt (foreign-function math-lib 'cbrt 'double 'double))
      (define pow (foreign-function math-lib 'pow 'double 'double 'double))
      (define lround (foreign-function math-lib 'lround 'long 'double))
      (test 3 (lround (cbrt 27.0)))
      (test 1024.0 (pow 2.0 10.0))

      (define strlen (foreign-function c-lib 'strlen 'long 'string))
      (define strtod (foreign-function c-lib 'strtod 'double 'string 'pointer))
      (define getpid (foreign-function c-lib (c-name 'getpid '_getpid) 'long))
      (test 5 (strlen "hello"))
      (test 3.5 (strtod "3.5" 0))
      (test #t (exact? (getpid)))

      ;; string results are copied out of C, and a null pointer is #f
      (define getenv (foreign-function c-lib 'getenv 'string 'string))
      (define path-name (c-name "PATH" "PATH"))
      (test #t (string? (getenv path-name)))
      (test #f (getenv "GOSCHEME_NO_SUCH_VARIABLE_XYZ"))

      ;; pointer results stay addresses: usable as arguments, and #f when null
      (define strchr (foreign-function c-lib 'strchr 'pointer 'string 'long))
      (test #t (exact? (strchr "hello" 108)))  ; the 'l'
      (test #f (strchr "hello" 122))           ; no 'z'
      ;; A pointer into memory C itself owns survives the call that produced it,
      ;; so it can be handed to another function.  (Pointers into a string
      ;; argument do not: those buffers are freed on return.)
      (define strlen-pointer (foreign-function c-lib 'strlen 'long 'pointer))
      (define getenv-address (foreign-function c-lib 'getenv 'pointer 'string))
      (define path (getenv path-name))
      (test (string-length path) (strlen-pointer (getenv-address path-name)))

      (test #t (foreign-library? math-lib))
      (test #f (foreign-library? 5))
      (test (string-append "#<foreign-library " math-lib-name ">")
            (let ((o (open-output-string)))
              (write math-lib o)
              (get-output-string o)))
      (test 'missing (guard (e (#t 'missing)) (foreign-function math-lib 'no_such_symbol 'long)))
      (test 'no-library (guard (e (#t 'no-library)) (load-shared-library "libdoes-not-exist.so")))
      (test 'mixed (guard (e (#t 'mixed))
                     (foreign-function math-lib 'pow 'double 'double 'long))))
    (begin
      ;; Built without cgo: the names exist and explain how to get them.
      (test #t (guard (e (#t (string? (error-object-message e))))
                 (load-shared-library "libm.so.6")))))

(test-end)

(test-end)
