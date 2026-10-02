(import (common))
(define a 1) (define b 2)
(define (loop i acc) (if (= i 0) acc (loop (- i 1) (+ acc a b))))
(time-it "globals" (lambda () (loop 200000 0)))
