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
(test 'yes (cond-expand ((and r7rs (not windows)) 'yes) (else 'no)))
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

(test-end)
