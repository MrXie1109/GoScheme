;;; SPDX-License-Identifier: MIT
;;; A library that imports another library, so loading is recursive.
(define-library (lib math)
  (export square-of-sum)
  (import (scheme base) (lib greet))
  (begin
    (define (square-of-sum a b) (let ((s (+ a b))) (* s s)))))
