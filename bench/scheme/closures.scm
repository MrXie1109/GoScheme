(define (make n) (lambda (x) (+ x n)))
(define (run i acc)
(if (= i 0) acc (run (- i 1) (+ acc ((make i) 1)))))
(run 20000 0)
