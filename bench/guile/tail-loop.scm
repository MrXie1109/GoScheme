(import (common))
(define (loop i acc) (if (= i 0) acc (loop (- i 1) (+ acc i))))
(time-it "tail-loop" (lambda () (loop 200000 0)))
