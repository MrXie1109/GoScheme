(import (common))
(define (build i acc) (if (= i 0) acc (build (- i 1) (cons i acc))))
(define (sum l acc) (if (null? l) acc (sum (cdr l) (+ acc (car l)))))
(time-it "lists" (lambda () (sum (build 100000 '()) 0)))
