(define v (make-vector 1000 0))
(define (fill i)
(if (= i 1000) 'done
(begin (vector-set! v i i) (fill (+ i 1)))))
(define (sum i acc)
(if (= i 1000) acc (sum (+ i 1) (+ acc (vector-ref v i)))))
(fill 0)
(let outer ((n 0) (total 0))
(if (= n 100) total (outer (+ n 1) (+ total (sum 0 0)))))
