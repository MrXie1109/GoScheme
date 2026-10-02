(import (common))
(define (make n) (lambda (x) (+ x n)))
(define (run i acc) (if (= i 0) acc (run (- i 1) (+ acc ((make i) 1)))))
(time-it "closures" (lambda () (run 20000 0)))
