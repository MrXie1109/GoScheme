(define (loop i acc) (if (= i 0) acc (loop (- i 1) (+ acc i))))
(loop 200000 0)
