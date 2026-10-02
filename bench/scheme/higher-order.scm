(define (fold f init l)
(if (null? l) init (fold f (f init (car l)) (cdr l))))
(define (build i acc) (if (= i 0) acc (build (- i 1) (cons i acc))))
(fold (lambda (a b) (+ a b)) 0 (build 20000 '()))
