(import (common))
(define (fib n) (if (< n 2) n (+ (fib (- n 1)) (fib (- n 2)))))
(time-it "fib" (lambda () (fib 20)))
