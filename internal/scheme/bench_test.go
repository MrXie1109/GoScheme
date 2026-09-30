// SPDX-License-Identifier: MIT

package scheme

import "testing"

// The benchmarks are the ones the README quotes and the ones the hot paths
// show up in: a recursive call chain, a tail loop, arithmetic in a loop, list
// building and a string loop.
var benchPrograms = map[string]string{
	"Fib":      `(define (fib n) (if (< n 2) n (+ (fib (- n 1)) (fib (- n 2))))) (fib 22)`,
	"TailLoop": `(let loop ((i 0) (sum 0)) (if (= i 500000) sum (loop (+ i 1) (+ sum 1))))`,
	"Arith":    `(let loop ((i 0) (acc 0)) (if (= i 200000) acc (loop (+ i 1) (if (even? i) (+ acc i) (- acc i)))))`,
	"Lists":    `(let loop ((i 0) (l '())) (if (= i 20000) (length l) (loop (+ i 1) (cons i l))))`,
	"Strings":  `(let loop ((i 0) (s "")) (if (= i 2000) (string-length s) (loop (+ i 1) (string-append s "x"))))`,
	"Closures": `(define (make-adder n) (lambda (x) (+ x n))) (let loop ((i 0) (f (make-adder 1))) (if (= i 200000) (f 0) (loop (+ i 1) (make-adder i))))`,
}

func BenchmarkPrograms(b *testing.B) {
	for name, src := range benchPrograms {
		src := src
		b.Run(name, func(b *testing.B) {
			m := NewMachine()
			if _, err := m.EvalString(src); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m := NewMachine()
				if _, err := m.EvalString(src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The (goscheme fast) library exists to be faster than the equivalent Scheme,
// so the pairs below measure exactly that: a merge sort and a filter written in
// Scheme against the Go versions, and a character loop against string-contains.
var fastPrograms = map[string]string{
	// Each pair builds its input the same way (with (goscheme fast) helpers) so
	// that the only difference measured is the operation under test.
	"SortScheme": `(import (scheme base) (goscheme fast) (scheme write))
	 (define (merge a b less?)
	   (cond ((null? a) b) ((null? b) a)
	         ((less? (car b) (car a)) (cons (car b) (merge a (cdr b) less?)))
	         (else (cons (car a) (merge (cdr a) b less?)))))
	 (define (split l)
	   (let loop ((slow l) (fast l) (acc '()))
	     (if (or (null? fast) (null? (cdr fast))) (values (reverse acc) slow)
	         (loop (cdr slow) (cddr fast) (cons (car slow) acc)))))
	 (define (msort l less?)
	   (if (or (null? l) (null? (cdr l))) l
	       (call-with-values (lambda () (split l))
	         (lambda (left right) (merge (msort left less?) (msort right less?) less?)))))
	 (car (msort (reverse (iota 1500)) <))`,
	"SortFast": `(import (scheme base) (goscheme fast))
	 (car (sort (reverse (iota 1500))))`,

	"FilterScheme": `(import (scheme base) (goscheme fast))
	 (length (let loop ((l (iota 20000)) (acc '()))
	           (cond ((null? l) acc)
	                 ((even? (car l)) (loop (cdr l) (cons (car l) acc)))
	                 (else (loop (cdr l) acc)))))`,
	"FilterFast": `(import (scheme base) (goscheme fast))
	 (length (filter even? (iota 20000)))`,

	"IotaScheme": `(import (scheme base))
	 (let loop ((i 0) (acc '())) (if (= i 20000) (length acc) (loop (+ i 1) (cons i acc))))`,
	"IotaFast": `(import (scheme base) (goscheme fast)) (length (iota 20000))`,

	"ContainsScheme": `(import (scheme base) (scheme char))
	 (define hay (make-string 20000 #\x))
	 (define (find s sub)
	   (let ((n (string-length s)) (m (string-length sub)))
	     (let loop ((i 0))
	       (cond ((> (+ i m) n) #f)
	             ((string=? (substring s i (+ i m)) sub) i)
	             (else (loop (+ i 1)))))))
	 (find hay "xxxy")`,
	"ContainsFast": `(import (scheme base) (goscheme fast))
	 (string-contains (make-string 20000 #\x) "xxxy")`,

	// Batch arithmetic: one Go pass over the whole vector against the same loop
	// written out one element at a time.
	"VectorAddScheme": `(import (scheme base) (goscheme fast))
	 (define a (vector-iota 20000))
	 (define b (vector-iota 20000 1))
	 (define out (make-vector 20000 0))
	 (let loop ((i 0))
	   (if (= i 20000) (vector-ref out 19999)
	       (begin (vector-set! out i (+ (vector-ref a i) (vector-ref b i)))
	              (loop (+ i 1)))))`,
	"VectorAddFast": `(import (scheme base) (goscheme fast))
	 (vector-ref (vector-add (vector-iota 20000) (vector-iota 20000 1)) 19999)`,

	// Number theory: trial division in Scheme against the Go sieve.
	"PrimesScheme": `(import (scheme base))
	 (define (prime? n)
	   (and (> n 1)
	        (let loop ((d 2))
	          (cond ((> (* d d) n) #t)
	                ((= 0 (modulo n d)) #f)
	                (else (loop (+ d 1)))))))
	 (let loop ((i 2) (count 0))
	   (if (= i 30000) count (loop (+ i 1) (if (prime? i) (+ count 1) count))))`,
	"PrimesFast": `(import (scheme base) (goscheme fast)) (length (primes 30000))`,

	// Deduplication: the O(n^2) member scan against the keyed Go pass.
	"DedupeScheme": `(import (scheme base) (goscheme fast))
	 (define (dedupe l)
	   (let loop ((l l) (out '()))
	     (cond ((null? l) (reverse out))
	           ((member (car l) out) (loop (cdr l) out))
	           (else (loop (cdr l) (cons (car l) out))))))
	 (length (dedupe (map (lambda (i) (modulo i 500)) (iota 4000))))`,
	"DedupeFast": `(import (scheme base) (goscheme fast))
	 (length (delete-duplicates (map (lambda (i) (modulo i 500)) (iota 4000))))`,
}

func BenchmarkFast(b *testing.B) {
	for name, src := range fastPrograms {
		src := src
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				m := NewMachine()
				if _, err := m.EvalString(src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
