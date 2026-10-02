package scheme

import "testing"

const fibSrc = `(define (fib n) (if (< n 2) n (+ (fib (- n 1)) (fib (- n 2))))) (fib 20)`

func BenchmarkFibVM(b *testing.B) {
	for i := 0; i < b.N; i++ {
		m := NewMachine()
		if _, err := m.EvalString(fibSrc); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFibInterp(b *testing.B) {
	for i := 0; i < b.N; i++ {
		m := NewMachine()
		m.Interpret = true
		if _, err := m.EvalString(fibSrc); err != nil {
			b.Fatal(err)
		}
	}
}
