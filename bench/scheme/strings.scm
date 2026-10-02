(define (build i acc)
(if (= i 0) acc (build (- i 1) (string-append acc "x"))))
(string-length (build 3000 ""))
