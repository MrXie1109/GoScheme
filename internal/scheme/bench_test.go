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
