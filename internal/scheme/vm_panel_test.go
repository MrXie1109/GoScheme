// SPDX-License-Identifier: MIT

package scheme

import "testing"

// The benchmark panel: the same programs run by the bytecode VM and by the
// tree-walker, so that the two execution paths can be compared on more than one
// shape of work.  The numbers are in docs/performance.md; this is where they
// come from.
var panelPrograms = map[string]string{
	// Non-tail recursion with arithmetic in the body.
	"fib": `(define (fib n) (if (< n 2) n (+ (fib (- n 1)) (fib (- n 2)))))
	        (fib 20)`,

	// A loop whose call is in tail position: no stack growth either way.
	"tail-loop": `(define (loop i acc) (if (= i 0) acc (loop (- i 1) (+ acc i))))
	              (loop 200000 0)`,

	// Creating and calling closures: one allocation per closure in both paths.
	"closures": `(define (make n) (lambda (x) (+ x n)))
	             (define (run i acc)
	               (if (= i 0) acc (run (- i 1) (+ acc ((make i) 1)))))
	             (run 20000 0)`,

	// Lots of global references: the interpreter caches the operator it just
	// looked up, the VM looks each one up again unless it is told otherwise.
	"globals": `(define a 1) (define b 2)
	            (define (loop i acc)
	              (if (= i 0) acc (loop (- i 1) (+ acc a b))))
	            (loop 200000 0)`,

	// The same shape with let-bound locals instead of globals.
	"locals": `(define (loop i)
	             (let ((a 1) (b 2))
	               (let inner ((j i) (acc 0))
	                 (if (= j 0) acc (inner (- j 1) (+ acc a b))))))
	           (loop 200000)`,

	// Building a list and walking it: cons, car, cdr, null? in a loop.
	"lists": `(define (build i acc) (if (= i 0) acc (build (- i 1) (cons i acc))))
	          (define (sum l acc) (if (null? l) acc (sum (cdr l) (+ acc (car l)))))
	          (sum (build 100000 '()) 0)`,

	// Vector reads and writes in a loop.
	"vectors": `(define v (make-vector 1000 0))
	            (define (fill i)
	              (if (= i 1000) 'done
	                  (begin (vector-set! v i i) (fill (+ i 1)))))
	            (define (sum i acc)
	              (if (= i 1000) acc (sum (+ i 1) (+ acc (vector-ref v i)))))
	            (fill 0)
	            (let outer ((n 0) (total 0))
	              (if (= n 100) total (outer (+ n 1) (+ total (sum 0 0)))))`,

	// String building: allocation-dominated in both paths.
	"strings": `(define (build i acc)
	             (if (= i 0) acc (build (- i 1) (string-append acc "x"))))
	           (string-length (build 3000 ""))`,

	// Higher-order code: a fold written with a closure over a list.
	"higher-order": `(define (fold f init l)
	                  (if (null? l) init (fold f (f init (car l)) (cdr l))))
	                (define (build i acc) (if (= i 0) acc (build (- i 1) (cons i acc))))
	                (fold (lambda (a b) (+ a b)) 0 (build 20000 '()))`,

	// A tiny evaluator over a small AST: the shape of a real Scheme program.
	"mini-eval": `(define (ev e env)
	               (cond ((number? e) e)
	                     ((symbol? e) (cdr (assq e env)))
	                     ((eq? (car e) 'add) (+ (ev (cadr e) env) (ev (caddr e) env)))
	                     ((eq? (car e) 'mul) (* (ev (cadr e) env) (ev (caddr e) env)))
	                     (else (ev (cadddr e) (cons (cons (cadr e) (ev (caddr e) env)) env)))))
	             (define (loop i acc)
	               (if (= i 0) acc
	                   (loop (- i 1)
	                         (+ acc (ev '(add 1 (mul 2 (let x 3 (add x x)))) '())))))
	             (loop 5000 0)`,

	// Merge sort of a list: list-heavy recursion.
	"sort": `(define (merge a b)
	           (cond ((null? a) b) ((null? b) a)
	                 ((< (car b) (car a)) (cons (car b) (merge a (cdr b))))
	                 (else (cons (car a) (merge (cdr a) b)))))
	         (define (split l)
	           (let loop ((slow l) (fast l) (acc '()))
	             (if (or (null? fast) (null? (cdr fast))) (values (reverse acc) slow)
	                 (loop (cdr slow) (cddr fast) (cons (car slow) acc)))))
	         (define (msort l)
	           (if (or (null? l) (null? (cdr l))) l
	               (call-with-values (lambda () (split l))
	                 (lambda (a b) (merge (msort a) (msort b))))))
	         (define (build i acc) (if (= i 0) acc (build (- i 1) (cons (modulo (* i 7919) 2000) acc))))
	         (car (msort (build 1000 '())))`,

	// Generators through call/cc: mostly interpreted by design.
	"callcc": `(define (count-to n)
	             (define k #f)
	             (define i 0)
	             (define v (call/cc (lambda (c) (set! k c) 0)))
	             (set! i (+ i 1))
	             (if (< i n) (k (+ v 1)) v))
	           (count-to 20000)`,
}

func benchPanel(b *testing.B, src string, interpret bool) {
	b.Helper()
	forms, err := NewStringReader(src).ReadAll()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := NewMachine()
		m.Interpret = interpret
		// The call the command line makes, so that the compiled column is the
		// path a program actually takes.
		if _, err := m.RunFormsCompiled(forms, m.Global); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPanel(b *testing.B) {
	for name, src := range panelPrograms {
		src := src
		b.Run(name+"/vm", func(b *testing.B) { benchPanel(b, src, false) })
		b.Run(name+"/interp", func(b *testing.B) { benchPanel(b, src, true) })
	}
}
