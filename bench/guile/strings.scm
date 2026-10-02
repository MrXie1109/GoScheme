(import (common))
(define (build i acc) (if (= i 0) acc (build (- i 1) (string-append acc "x"))))
(time-it "strings" (lambda () (string-length (build 3000 ""))))
