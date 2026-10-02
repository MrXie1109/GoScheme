// SPDX-License-Identifier: MIT

package scheme

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bytecode VM must be indistinguishable from the tree-walker.  These tests
// are the evidence: every program below runs three ways — compiled, compiled
// and written to bytes and read back, and in the tree-walker — and the three
// have to print the same thing.

// vmPrograms is the corpus.  It deliberately mixes what the compiler handles
// (if, let, lambda, closures, set!, the derived forms, tail calls) with what it
// declines and the interpreter runs (macro definitions, records, guard,
// parameterize, match, do), because the point is that the boundary is
// invisible.
var vmPrograms = []struct {
	name string
	src  string
}{
	{"arithmetic", `(display (+ 1 2)) (display (* 3 4)) (newline)`},
	{"named-let-tail", `(display (let loop ((i 0) (acc 0))
	                            (if (= i 100000) acc (loop (+ i 1) (+ acc i))))) (newline)`},
	{"mutual-letrec", `(display (letrec ((even? (lambda (n) (if (= n 0) #t (odd? (- n 1)))))
	                                     (odd? (lambda (n) (if (= n 0) #f (even? (- n 1))))))
	                            (even? 1000))) (newline)`},
	{"closure-counter", `(define (counter)
	                       (let ((n 0)) (lambda () (set! n (+ n 1)) n)))
	                     (define c (counter))
	                     (c) (c)
	                     (display (c)) (newline)`},
	{"closure-capture-loop", `(define fs '())
	                          (do ((i 0 (+ i 1))) ((= i 3))
	                            (set! fs (cons (let ((j i)) (lambda () j)) fs)))
	                          (display (map (lambda (f) (f)) fs)) (newline)`},
	{"set-global", `(define x 1)
	                (define (bump) (set! x (+ x 1)) x)
	                (bump)
	                (display (bump)) (newline)`},
	{"internal-defines", `(define (f n)
	                       (define m (* n 2))
	                       (define (g) (+ m 1))
	                       (g))
	                     (display (f 5)) (newline)`},
	{"let-star", `(display (let* ((x 2) (y (* x x)) (z (+ x y))) (list x y z))) (newline)`},
	{"cond-arrow", `(display (cond ((assv 2 '((1 . a) (2 . b))) => cdr) (else 'none))) (newline)`},
	{"case-arrow", `(display (case 3 ((1 2) 'low) ((3 4) => (lambda (k) (list 'high k))) (else 'other))) (newline)`},
	{"and-or", `(display (list (and 1 2) (and) (or #f 3) (or) (and 1 #f))) (newline)`},
	{"when-unless", `(display (list (when #t 'yes) (when #f 'no) (unless #f 'ok))) (newline)`},
	// do compiles now: it is an expansion into letrec and guard, and (continue)
	// keeps its dynamic meaning because the guard recognises the same private
	// condition, even when it is raised from a procedure the body called.
	{"do-continue-from-procedure", `(define (skip? i) (if (odd? i) (continue) #f))
	                                 (define kept '())
	                                 (do ((i 0 (+ i 1))) ((= i 6) (display (reverse kept)))
	                                   (skip? i)
	                                   (set! kept (cons i kept)))
	                                 (newline)`},
	{"do-shapes", `(display (do ((i 0 (+ i 1)) (acc '() (cons i acc))) ((= i 4) (reverse acc))))
	               (newline)
	               (display (do ((x 0 (+ x 1))) ((= x 0) 'done)))
	               (newline)
	               (display (do ((v (make-vector 3 0)) (i 0 (+ i 1)))
	                            ((= i 3) (vector->list v))
	                          (vector-set! v i (* i i))))
	               (newline)
	               (display (let ((n 0))
	                          (do ((i 0 (+ i 1))) ((= i 3) n)
	                            (set! n (+ n 1)))
	                          n))
	               (newline)`},
	// A parameter's converter runs once on the way in and must not run again on
	// the way out, delay's thunk runs once however often it is forced, and a
	// case-lambda picks its clause by arity.
	{"parameterize", `(define p2 (make-parameter 1))
	                   (display (list (p2) (parameterize ((p2 2)) (p2)) (p2))) (newline)
	                   (define c (make-parameter 10 (lambda (x) (* x 2))))
	                   (display (list (c) (parameterize ((c 3)) (c)) (c))) (newline)
	                   (display (parameterize ((p2 5))
	                              (dynamic-wind (lambda () (display "in "))
	                                            (lambda () (p2))
	                                            (lambda () (display "out "))))) (newline)`},
	{"delay", `(define pr (delay (begin (display "once ") 7)))
	             (display (list (force pr) (force pr))) (newline)
	             (display (force (delay-force (delay 9)))) (newline)`},
	{"case-lambda", `(define f (case-lambda (() 'zero)
	                                       ((x) (list 'one x))
	                                       ((x y . z) (list 'many x y z))))
	                 (display (list (f) (f 1) (f 1 2) (f 1 2 3))) (newline)`},
	// A thread's body is compiled now, so the channel handshake it takes part
	// in has to work exactly as it did; the programs below are written so that
	// the order of what they print is fixed by the channels themselves.
	{"go", `(define ch (make-channel 1))
	          (go (chan-send! ch (+ 1 2)))
	          (display (chan-recv! ch)) (newline)
	          (define c (make-channel))
	          (define order (make-channel))
	          (go (chan-send! c 'first) (chan-send! order 'sent))
	          (display (list (chan-recv! c) (chan-recv! order))) (newline)
	          (define shared 0)
	          (define guards (make-mutex))
	          (let loop ((i 0))
	            (if (< i 50)
	                (begin (go (with-mutex guards (set! shared (+ shared 1))))
	                       (loop (+ i 1)))))
	          (go-wait)
	          (display shared) (newline)`},
	// let-values collects each producer's values into temporaries before it
	// binds, so no producer sees a binding — which is the whole difference
	// from let*-values, and the reason a body of its own is needed when there
	// are no bindings at all.
	{"let-values", `(display (let-values (((a b) (values 1 2)) ((c) (values 3))) (list a b c))) (newline)
	                (display (let*-values (((a b) (values 1 2)) ((c) (values (+ a b)))) (list a b c))) (newline)
	                (display (let ((x 'outer))
	                           (let-values (((x) (values 'inner)) ((y) x)) (list x y)))) (newline)
	                (display (let ((x 'outer))
	                           (let*-values (((x) (values 'inner)) ((y) x)) (list x y)))) (newline)
	                (display (let ((x 1)) (let*-values () (define x 2) #f) x)) (newline)
	                (display (let-values ((r (values 1 2 3))) r)) (newline)
	                (display (let-values (((a b . r) (values 1 2 3 4))) (list a b r))) (newline)
	                (display (guard (e (#t 'few)) (let-values (((a b) (values 1))) a))) (newline)
	                (display (guard (e (#t 'many)) (let-values (((a) (values 1 2))) a))) (newline)`},
	// define-values binds every value of an expression, at the top level or in
	// a body (where a closure must see the same binding), and
	// define-record-type builds a type whose procedures are ordinary
	// procedures however they were created.
	{"define-values", `(define-values (a b) (values 1 2))
	                    (display (list a b)) (newline)
	                    (define-values (x . rest) (values 1 2 3))
	                    (display (list x rest)) (newline)
	                    (define-values all (values 1 2 3))
	                    (display all) (newline)
	                    (display (guard (e (#t 'err)) (define-values (p q) (values 1)) 'no)) (newline)
	                    (define (counter) (define-values (n) (values 0)) (lambda () (set! n (+ n 1)) n))
	                    (define c1 (counter)) (define c2 (counter))
	                    (c1) (c1)
	                    (display (list (c1) (c2))) (newline)`},
	{"define-record-type", `(define-record-type point (make-point x y) point?
	                                              (x point-x set-point-x!) (y point-y))
	                       (define p (make-point 3 4))
	                       (display (list (point? p) (point-x p) (point-y p) (point? 5))) (newline)
	                       (set-point-x! p 9)
	                       (display (point-x p)) (newline)
	                       (display (let ()
	                                  (define-record-type box (mk-box v) box? (v unbox))
	                                  (unbox (mk-box 7)))) (newline)`},
	// select runs in another thread's timing, so the clauses here are the ones
	// whose outcome does not depend on it: a channel that is already ready,
	// and an else.
	{"select", `(define ch (make-channel 1))
	             (chan-send! ch 'ready)
	             (display (select (chan-recv! ch) => (lambda (v) (list 'got v)))) (newline)
	             (define closed9 (make-channel 1))
	             (chan-close! closed9)
	             (display (select (chan-recv! closed9) => (lambda (v) (list 'closed v)))) (newline)
	             (display (select (chan-recv! (make-channel)) => (lambda (v) 'never)
	                              (else) => (lambda () 'else))) (newline)`},
	// match compiles too: the patterns are matched by the same code the
	// interpreter uses, and what compiles is the guard and the body of each
	// clause, with the pattern's variables as their parameters.
	{"match", `(display (list (match (list 1 2 3) ((a b c) (list 'three a b c)) ((_ ... rest) 'longer))
	                          (match 5 (1 'one) (5 'five) (else 'other))
	                          (match '(1 2 . 3) ((a b . r) (list a b r)))
	                          (match #(1 2) (#(a b) (+ a b)) (else 'no))
	                          (match 'sym ((quote sym) 'quoted) (else 'no))
	                          (match 4 ((and (not 1) x) (list 'not-one x)))
	                          (match 3 ((or 1 3) 'one-or-three) (else 'no))
	                          (match 9 (x (guard (> x 5)) (list 'big x)) (x (list 'small x)))
	                          (match 2 (x (guard (> x 5)) (list 'big x)) (x (list 'small x))))) (newline)
	           (display (guard (e (#t 'nomatch)) (match 'zzz (1 'one)))) (newline)`},
	// cond-expand chooses a clause while compiling, and a macro defined inside
	// a body is expanded there: a template that mentions an outer variable has
	// to mean that variable, not one the use site happens to have.
	{"cond-expand-and-lexical-macros", `(display (cond-expand ((and r7rs (not (library (nope)))) 'r7rs)
	                                                       (else 'other))) (newline)
	                                    (display (let () (define-syntax m (syntax-rules () ((_ x) (list 'm x)))) (m 1))) (newline)
	                                    (display (let-syntax ((ls (syntax-rules () ((_ x) (list 'ls x))))) (ls 2))) (newline)
	                                    (display (letrec-syntax ((lrs (syntax-rules () ((_ x) (list 'lrs x))))) (lrs 3))) (newline)
	                                    (define (f x)
	                                      (define-syntax double (syntax-rules () ((_ e) (* 2 e))))
	                                      (double x))
	                                    (display (f 21)) (newline)
	                                    (display (let ((x 'var))
	                                               (let-syntax ((x (syntax-rules () ((_ e) (list 'macro e)))))
	                                                 (x 9)))) (newline)`},
	{"assert", `(display (assert (+ 1 1))) (newline)
	            (display (guard (e (#t (error-object-message e))) (assert #f))) (newline)`},
	{"do-continue", `(define kept '())
	                 (do ((i 0 (+ i 1))) ((= i 6) (display (reverse kept)))
	                   (if (odd? i) (continue))
	                   (set! kept (cons i kept)))
	                 (newline)`},
	{"callcc-escape", `(display (call/cc (lambda (k) (+ 1 (k 42))))) (newline)`},
	{"cond-plain", `(display (cond ((= 1 2) 'a) ((= 1 1) 'b) (else 'c))) (newline)`},
	{"cond-no-else", `(display (cond ((= 1 2) 'a) ((= 1 3) 'b))) (newline)`},
	{"cond-test-only", `(display (cond ((assv 2 '((1 . a))) => cdr) ((+ 1 1)) (else 'no))) (newline)`},
	{"case-multi", `(display (list (case 'b ((a b) 'yes) (else 'no))
	                                (case 'z ((a b) 'yes) (else 'no)))) (newline)`},
	{"and-or-values", `(display (list (and 1 2 3) (and 1 #f 3) (and) (or #f #f 5) (or #f) (or))) (newline)`},
	{"nested-binding", `(display (let ((x 1))
	                            (let ((y 2))
	                              (let* ((x 10) (z (+ x y)))
	                                (letrec ((f (lambda () (list x y z))))
	                                  (f)))))) (newline)`},
	{"shadowing", `(define x 'global)
	               (display (list x (let ((x 'outer)) (let ((x 'inner)) x)) x)) (newline)`},
	{"set-from-closure", `(define (make) (let ((n 0)) (lambda () (set! n (+ n 1)) n)))
	                      (define a (make))
	                      (define b (make))
	                      (a) (a)
	                      (display (list (a) (b))) (newline)`},
	// A macro that defines a macro: the form after it can only be compiled
	// once the macro it defines exists, so the compiler has to notice that it
	// is a form that teaches it something.
	{"macro-defining-macro", `(define-syntax be-like-begin
	                           (syntax-rules ()
	                             ((_ name)
	                              (define-syntax name
	                                (syntax-rules ()
	                                  ((name expr (... ...))
	                                   (begin expr (... ...))))))))
	                         (be-like-begin sequence)
	                         (display (sequence 0 1 2 3)) (newline)`},
	// => bound as a variable is not the auxiliary keyword: this is a cond
	// clause with two expressions, and the compiled path used to apply 'ok.
	{"aux-shadowed", `(display (let ((=> #f)) (cond (#t => 'ok)))) (newline)
	                  (display (case 1 ((1) => (lambda (x) (+ x 1))) (else 'no))) (newline)`},
	// Quasiquote is expanded by the compiler into the constructors the
	// interpreter's own expander produces, including the nested and splicing
	// cases where the depth decides what an unquote means.
	{"quasiquote", `(define name 'world)
	                 (define xs '(1 2 3))
	                 (write ` + "`" + `(hello ,name ,@xs (nested ,(+ 1 2)) #(a ,name))) (newline)
	                 (write ` + "`" + `(a ` + "`" + `(b ,(c ,name)))) (newline)
	                 (write ` + "`" + `` + "`" + `(a ,(+ 1 2))) (newline)
	                 (write (quasiquote (list (unquote (+ 1 2)) 4))) (newline)`},
	{"callcc-across-steps", `(define k #f)
	                        (define n 0)
	                        (define v (call/cc (lambda (c) (set! k c) 1)))
	                        (do ((i 0 (+ i 1))) ((= i 0)))
	                        (set! n (+ n 1))
	                        (if (< n 3) (k (+ v 1)) (begin (display v) (newline)))`},
	{"callcc-multishot", `(define k #f)
	                      (define n 0)
	                      (define v (call/cc (lambda (c) (set! k c) 1)))
	                      (set! n (+ n 1))
	                      (if (< n 3) (k (+ v 1)) (begin (display v) (newline)))`},
	{"guard-raise", `(display (guard (e (#t (list 'caught (error-object-message e))))
	                           (error "boom"))) (newline)`},
	// guard is compiled now, so its clauses, its `=>`, the shadowing of the
	// condition variable and a guard nested inside a primitive's callback all
	// have to agree with the tree-walker.
	{"guard-clauses", `(define (try thunk)
	                     (guard (e ((symbol? e) (list 'sym e))
	                               ((string? e) (list 'str e))
	                               ((and (pair? e) (assv 'b e)) => cdr)
	                               (else (list 'other e)))
	                       (thunk)))
	                   (display (list (try (lambda () (raise 'x)))
	                                  (try (lambda () (raise "s")))
	                                  (try (lambda () (raise '((a . 1) (b . 2)))))
	                                  (try (lambda () (raise 7)))
	                                  (try (lambda () 'quiet)))) (newline)
	                   (display (let ((x 'outer)) (try (lambda () (raise x))))) (newline)
	                   (display (map (lambda (v) (try (lambda () (raise v)))) '(a b))) (newline)`},
	{"guard-match", `(display (list (match (list 1 2 3) ((a b c) (+ a b c)) ((_ ... rest) 'longer))
	                                (match 'z ((a b) 'pair) (else 'other)))) (newline)`},
	{"primitive-raises", `(define (boom) (+ 1 'a))
	                      (define (run) (boom))
	                      (display (guard (e (#t 'caught)) (run))) (newline)`},
	{"primitive-raises-toplevel", `(define (boom2) (car 5))
	                               (display (guard (e (#t 'caught)) (boom2) 'no)) (newline)`},
	{"deep-non-tail", `(define (sum n) (if (= n 0) 0 (+ n (sum (- n 1)))))
	                   (display (sum 200000)) (newline)`},
	{"values-many", `(display (call-with-values (lambda () (values 1 2 3 4 5 6))
	                                         (lambda args args))) (newline)`},
	{"dynamic-wind", `(define log '())
	                  (define (note x) (set! log (cons x log)))
	                  (dynamic-wind (lambda () (note 'in))
	                                (lambda () (call/cc (lambda (k) (note 'body))))
	                                (lambda () (note 'out)))
	                  (display (reverse log)) (newline)`},
	{"macro", `(define-syntax swap!
	             (syntax-rules () ((_ a b) (let ((tmp a)) (set! a b) (set! b tmp)))))
	           (define p 1) (define q 2)
	           (swap! p q)
	           (display (list p q)) (newline)`},
	{"letrec-early", `(display (guard (e (#t 'error))
	                           (letrec ((a (lambda () b)) (b (a))) 1))) (newline)`},
	{"multiple-values", `(display (call-with-values (lambda () (values 1 2 3))
	                                         (lambda (a b c) (+ a b c)))) (newline)`},
	{"records", `(define-record-type point (make-point x y) point? (x point-x) (y point-y))
	             (display (point-x (make-point 3 4))) (newline)`},
	{"parameterize", `(define p (make-parameter 1))
	                  (display (list (p) (parameterize ((p 2)) (p)) (p))) (newline)`},
	{"strings", `(display (list (string-append "a" "b") (string-length "héllo")
	                            (substring "hello" 1 3)
	                            (string->list "ab")))
	             (newline)`},
	{"vectors", `(define v (vector 1 2 3))
	             (vector-set! v 1 9)
	             (display (list (vector-ref v 1) (vector->list v))) (newline)`},
	{"hash-table", `(define h (make-equal-hashtable))
	                (hash-table-set! h 'a 1)
	                (display (hash-table-ref/default h 'a 'missing)) (newline)`},
	{"srfi1", `(display (list (fold + 0 '(1 2 3)) (take 2 '(1 2 3)) (delete-duplicates '(1 2 1)))) (newline)`},
	{"match", `(display (match (list 1 2 3)
	                       ((a b c) (+ a b c))
	                       ((_ ... rest) 'longer))) (newline)`},
	{"delay-force", `(define pr (delay (begin (display "once ") 7)))
	                 (display (list (force pr) (force pr))) (newline)`},
	{"deep-recursion", `(define (sum n) (if (= n 0) 0 (+ n (sum (- n 1)))))
	                    (display (sum 20000)) (newline)`},
	{"error-arity", `(display (guard (e (#t (error-object-message e))) ((lambda (x) x)))) (newline)`},
	{"error-unbound", `(display (guard (e (#t 'unbound)) (this-is-not-bound))) (newline)`},
	{"error-car", `(display (guard (e (#t 'bad)) (car 5))) (newline)`},
	{"error-set-unbound", `(display (guard (e (#t 'unbound)) (set! nope 1))) (newline)`},
}

// runProgramText evaluates src and returns what it printed.
func runProgramText(t *testing.T, src string, interpret bool) string {
	t.Helper()
	out := NewOutputStringPort()
	m := NewMachine()
	m.Interpret = interpret
	m.CurOut = out
	m.OutParam.values[0] = out
	// The same call the command line makes: compiled where possible, with a
	// group the compiler cannot take whole compiled form by form.
	forms, err := NewStringReader(src).ReadAll()
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if _, err := m.RunFormsCompiled(forms, m.Global); err != nil {
		t.Fatalf("evaluating: %v", err)
	}
	return out.OutputString()
}

// compileAndRun compiles src, serialises it, reads it back and runs that.
func compileAndRun(t *testing.T, src string) string {
	t.Helper()
	out := NewOutputStringPort()
	m := NewMachine()
	m.CurOut = out
	m.OutParam.values[0] = out
	r := NewStringReader(src)
	forms, err := r.ReadAll()
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	prog, err := CompileProgram(m, forms, m.Global)
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteBytecode(&buf, prog); err != nil {
		t.Fatalf("writing bytecode: %v", err)
	}
	loaded, err := ReadBytecode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("reading bytecode: %v", err)
	}
	m2 := NewMachine()
	m2.CurOut = out
	m2.OutParam.values[0] = out
	if _, err := m2.RunProgram(loaded, m2.Global); err != nil {
		t.Fatalf("running bytecode: %v", err)
	}
	return out.OutputString()
}

// TestVMDifferential is the contract: the same program prints the same thing
// compiled, compiled-and-reloaded, and interpreted.
func TestVMDifferential(t *testing.T) {
	for _, p := range vmPrograms {
		p := p
		t.Run(p.name, func(t *testing.T) {
			compiled := runProgramText(t, p.src, false)
			reloaded := compileAndRun(t, p.src)
			interpreted := runProgramText(t, p.src, true)
			if compiled != interpreted {
				t.Errorf("compiled and interpreted differ:\n compiled: %q\ninterpreted: %q",
					compiled, interpreted)
			}
			if reloaded != interpreted {
				t.Errorf("bytecode file and interpreted differ:\n reloaded: %q\ninterpreted: %q",
					reloaded, interpreted)
			}
			if compiled == "" {
				t.Errorf("the program printed nothing, so the test proves nothing")
			}
		})
	}
}

// TestVMReallyCompiles checks that the corpus is not quietly running in the
// interpreter: a compiled program must compile, and a form the compiler
// declines must not.
func TestVMReallyCompiles(t *testing.T) {
	m := NewMachine()
	compiled, err := compileTop(m, mustRead(t, `(define (f n) (if (< n 2) n (+ (f (- n 1)) (f (- n 2)))))`), m.Global)
	if err != nil {
		t.Fatalf("a plain recursive definition should compile: %v", err)
	}
	// The top-level form is a closure, a define and a return; the body of f is
	// a Code of its own in the constant pool.
	var body *Code
	for _, k := range compiled.Consts {
		if sub, ok := k.(*Code); ok && sub.NParams == 1 {
			body = sub
		}
	}
	if body == nil {
		t.Fatalf("the lambda was not compiled into a Code of its own")
	}
	if len(body.Instrs) < 8 {
		t.Fatalf("the compiled body is suspiciously small: %d instructions", len(body.Instrs))
	}
	// Every form the language has compiles now, so this is the list that used
	// to be "left to the interpreter": it is a guard against one of them
	// quietly going back.
	for _, src := range []string{
		`(do ((i 0 (+ i 1))) ((= i 1) i))`,
		`(guard (e (#t e)) (error "x"))`,
		"`(a ,(+ 1 2))",
		`(assert #t)`,
		`(delay 1)`,
		`(case-lambda (() 1) ((x) x))`,
		`(parameterize ((p 1)) p)`,
		`(let-values (((a b) (values 1 2))) a)`,
		`(let*-values (((a) (values 1))) a)`,
		`(go 1)`,
		`(define-values (a b) (values 1 2))`,
		`(define-record-type p (make-p x) p? (x p-x))`,
		`(match 1 (1 'one))`,
		`(select (else) => 'x)`,
		`(cond-expand (else 1))`,
		`(let-syntax ((m (syntax-rules () ((_ x) x)))) (m 1))`,
		`(letrec-syntax ((m (syntax-rules () ((_ x) x)))) (m 1))`,
		`(let () (define-syntax m (syntax-rules () ((_ x) x))) (m 1))`,
	} {
		if _, err := compileTop(m, mustRead(t, src), m.Global); err != nil {
			t.Errorf("%s should compile: %v", src, err)
		}
	}

	// A group the compiler can only take form by form becomes a chunk of
	// steps, which run in one extent.  Nothing in the language forces that any
	// more, so the chunk is built here rather than found in a program.
	stepCode, err := compileTop(m, mustRead(t, `(display "a")`), m.Global)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	out := NewOutputStringPort()
	run := NewMachine()
	run.CurOut = out
	run.OutParam.values[0] = out
	prog := &Program{Chunks: []Chunk{{Steps: []Chunk{
		{Code: stepCode},
		{Form: mustRead(t, `(display "b")`)},
	}}}}
	if _, err := run.RunProgram(prog, run.Global); err != nil {
		t.Fatalf("running steps: %v", err)
	}
	if got := out.OutputString(); got != "ab" {
		t.Errorf("the steps printed %q, want %q", got, "ab")
	}
	// Compiled code must not be produced when the machine is asked to
	// interpret everything.
	m.Interpret = true
	if code, _ := m.compile(mustRead(t, `(+ 1 2)`), m.Global); code != nil {
		t.Errorf("Interpret should turn compilation off")
	}
}

// A file loaded at run time whose top-level forms come out as a mixed group
// used to run nothing at all: the run of steps was taken for a form, and its
// form was nil.  The whole program was silently skipped, in both directions.
func TestLoadRunsMixedGroup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed.scm")
	src := "(define x 1)\n" +
		"(do ((i 0 (+ i 1))) ((= i 2)) (display \"d\"))\n" +
		"(display (+ x 1)) (newline)\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, interpret := range []bool{false, true} {
		out := NewOutputStringPort()
		m := NewMachine()
		m.Interpret = interpret
		m.CurOut = out
		m.OutParam.values[0] = out
		m.AddLoadPath(dir)
		// ToSlash: a Windows path in a Scheme string literal would read its
		// backslashes as escapes.
		form := mustRead(t, `(load "`+filepath.ToSlash(path)+`")`)
		if _, err := m.RunFormsCompiled([]Value{form}, m.Global); err != nil {
			t.Fatalf("interpret=%v: %v", interpret, err)
		}
		if got := out.OutputString(); got != "dd2\n" {
			t.Errorf("interpret=%v: loaded file printed %q, want %q", interpret, got, "dd2\n")
		}
	}
}

func mustRead(t *testing.T, src string) Value {
	t.Helper()
	forms, err := NewStringReader(src).ReadAll()
	if err != nil {
		t.Fatalf("reading %q: %v", src, err)
	}
	if len(forms) != 1 {
		t.Fatalf("expected one form, got %d", len(forms))
	}
	return forms[0]
}

// TestBytecodeFormat pins the parts of the format that a program may rely on:
// the magic number, the version, and a refusal to read anything else.
func TestBytecodeFormat(t *testing.T) {
	m := NewMachine()
	prog, err := CompileProgram(m, []Value{mustRead(t, `(display 1)`)}, m.Global)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteBytecode(&buf, prog); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), bytecodeMagic) {
		t.Errorf("the file does not start with %q", bytecodeMagic)
	}
	if _, err := ReadBytecode(strings.NewReader("not bytecode at all")); err == nil {
		t.Errorf("reading a non-bytecode file should fail")
	}
	// A future version must be refused rather than misread.
	bad := append([]byte(bytecodeMagic), bytecodeVersion+1)
	if _, err := ReadBytecode(bytes.NewReader(bad)); err == nil {
		t.Errorf("a file from another version should be refused")
	}
	// And a program with a literal structure must round trip exactly.
	src := `(define d '(1 (2 . 3) #(4 "five" #\x) #u8(1 2) 3/4 1.5 123456789012345678901234567890)) (display d)`
	if got, want := compileAndRun(t, src), runProgramText(t, src, true); got != want {
		t.Errorf("literals did not survive the round trip: %q vs %q", got, want)
	}
}

// TestVMTailCallsAreProper checks the VM's own tail-call path, which is what
// keeps a loop from growing the stack.
func TestVMTailCallsAreProper(t *testing.T) {
	m := NewMachine()
	out := NewOutputStringPort()
	m.CurOut = out
	m.OutParam.values[0] = out
	if _, err := m.EvalString(`
		(define (loop n) (if (= n 0) 'done (loop (- n 1))))
		(display (loop 1000000))`); err != nil {
		t.Fatal(err)
	}
	if out.OutputString() != "done" {
		t.Fatalf("got %q", out.OutputString())
	}
}
